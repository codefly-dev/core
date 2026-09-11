package ciinputs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"reflect"
	"strings"
	"testing"

	agent "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

var build = Key{Phase: agent.TaskPhase_TASK_PHASE_ARTIFACT_BUILD}
var unit = Key{Phase: agent.TaskPhase_TASK_PHASE_TEST, Suite: "unit"}
var integration = Key{Phase: agent.TaskPhase_TASK_PHASE_TEST, Suite: "integration"}

func input(kind agent.EffectiveInputKind, name, content string) *agent.EffectiveInput {
	sum := sha256.Sum256([]byte(content))
	return &agent.EffectiveInput{Kind: kind, Owner: "frontend", Name: name, Identity: &agent.EffectiveIdentity{Kind: agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_SHA256, Digest: hex.EncodeToString(sum[:])}}
}
func declaration(k Key, inputs ...*agent.EffectiveInput) *agent.TaskInputs {
	return &agent.TaskInputs{Task: &agent.TaskKey{Phase: k.Phase, Suite: k.Suite}, Complete: true, Inputs: append([]*agent.EffectiveInput{
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_PLUGIN, "agent", "resolved-agent"),
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_TOOLCHAIN, "toolchain", "resolved-toolchain"),
	}, inputs...)}
}
func response(tasks ...*agent.TaskInputs) *agent.GetEffectiveInputsResponse {
	return &agent.GetEffectiveInputsResponse{SchemaVersion: Version, Snapshot: "snapshot", Tasks: tasks}
}
func evaluate(t *testing.T, r *agent.GetEffectiveInputsResponse, keys ...Key) []Task {
	t.Helper()
	got, err := Evaluate(r, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "snapshot"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEffectiveConsumption(t *testing.T) {
	prod := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, "app.test.js", "imported production content")
	test := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, "tests/unit.js", "test content")
	api := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SERVICE_IMPLEMENTATION, "api", "v1")
	api.Owner = "api"
	r := response(declaration(build, prod), declaration(unit, prod, test), declaration(integration, prod, test, api))
	r.Tasks[2].RuntimeServices = []string{"api"}
	before := evaluate(t, r, build, unit, integration)
	for _, tc := range []struct {
		name   string
		change func(*agent.GetEffectiveInputsResponse)
		want   []Key
	}{
		{"test edit", func(r *agent.GetEffectiveInputsResponse) {
			for _, i := range []int{1, 2} {
				r.Tasks[i].Inputs[3] = input(test.Kind, test.Name, "changed")
			}
		}, []Key{integration, unit}},
		{"API implementation", func(r *agent.GetEffectiveInputsResponse) {
			r.Tasks[2].Inputs[4].Identity.Digest = strings.Repeat("a", 64)
		}, []Key{integration}},
		{"imported test-like production file", func(r *agent.GetEffectiveInputsResponse) {
			for _, task := range r.Tasks {
				task.Inputs[2].Identity.Digest = strings.Repeat("a", 64)
			}
		}, []Key{integration, unit, build}},
		{"removed input", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs = r.Tasks[0].Inputs[:2] }, []Key{build}},
		{"renamed input", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[2].Name = "moved.js" }, []Key{build}},
		{"removed dependency edge", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[2].Inputs = r.Tasks[2].Inputs[:4] }, []Key{integration}},
		{"changed runtime edge", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[2].RuntimeServices = []string{"other-api"} }, []Key{integration}},
		{"missing required suite", func(r *agent.GetEffectiveInputsResponse) { r.Tasks = r.Tasks[:2] }, []Key{integration}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := proto.Clone(r).(*agent.GetEffectiveInputsResponse)
			tc.change(candidate)
			got := Changed(before, evaluate(t, candidate, build, unit, integration))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changed=%v want %v", got, tc.want)
			}
		})
	}
	if got := Changed(before, before[:2]); len(got) != 1 || got[0] != build {
		t.Fatalf("removed inventory task lost: %v", got)
	}
}

func TestEveryInputKindInvalidatesConsumers(t *testing.T) {
	for kind := agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE; kind <= agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_EXTERNAL; kind++ {
		t.Run(kind.String(), func(t *testing.T) {
			r := response(declaration(build, input(kind, "shared", "old")), declaration(unit, input(kind, "shared", "old")))
			before := evaluate(t, r, build, unit)
			for _, task := range r.Tasks {
				task.Inputs[2] = input(kind, "shared", "new")
			}
			if len(Changed(before, evaluate(t, r, build, unit))) != 2 {
				t.Fatal("consumer lost")
			}
		})
	}
}

func TestCanonicalIdentityAndSnapshots(t *testing.T) {
	r := response(declaration(build, input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, "link", "target")))
	r.Tasks[0].Inputs[2].Path = true
	r.Tasks[0].Inputs[2].Mode = 0o120000
	r.Tasks[0].RuntimeServices = []string{"b", "a"}
	original := proto.Clone(r)
	before := evaluate(t, r, build)
	if !proto.Equal(original, r) {
		t.Fatal("mutated caller declaration")
	}
	r.Tasks[0].Inputs[0], r.Tasks[0].Inputs[2] = r.Tasks[0].Inputs[2], r.Tasks[0].Inputs[0]
	r.Tasks[0].RuntimeServices = []string{"a", "b"}
	if len(Changed(before, evaluate(t, r, build))) != 0 {
		t.Fatal("ordering affects identity")
	}
	r.Snapshot = "different-snapshot"
	other, err := Evaluate(r, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: r.Snapshot}, []Key{build})
	if err != nil {
		t.Fatal(err)
	}
	if len(Changed(before, other)) != 0 {
		t.Fatal("snapshot token affects task identity")
	}
	if _, err := Evaluate(r, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "wrong-snapshot"}, []Key{build}); err == nil {
		t.Fatal("accepted stale snapshot")
	}
	r.Snapshot = "snapshot"
	r.Tasks[0].Inputs[0].Mode = 0o100644
	if len(Changed(before, evaluate(t, r, build))) != 1 {
		t.Fatal("symlink mode change lost")
	}
	r.Tasks[0].Inputs[0] = input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, "link", "new-target")
	if len(Changed(before, evaluate(t, r, build))) != 1 {
		t.Fatal("symlink retarget lost")
	}
}

func TestCompatibilityAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *agent.GetEffectiveInputsResponse
	}{
		{"legacy", nil}, {"empty", &agent.GetEffectiveInputsResponse{}}, {"future", &agent.GetEffectiveInputsResponse{SchemaVersion: 2}}, {"missing", response()},
		{"incomplete", response(&agent.TaskInputs{Task: &agent.TaskKey{Phase: build.Phase}})},
		{"missing identities", response(&agent.TaskInputs{Task: &agent.TaskKey{Phase: build.Phase}, Complete: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluate(t, tc.response, build)
			if len(got) != 1 || !got[0].Conservative || got[0].CacheEligible || got[0].Identity != "" {
				t.Fatalf("unsafe fallback: %+v", got)
			}
		})
	}
	r := response(declaration(build))
	r.Tasks[0].ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 100, protowire.VarintType), 1))
	if !evaluate(t, r, build)[0].Conservative {
		t.Fatal("ignored unknown semantic field")
	}
	r = response(declaration(build, input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_EXTERNAL, "network", "")))
	r.Tasks[0].Inputs[2].Identity = nil
	if !evaluate(t, r, build)[0].Conservative {
		t.Fatal("reused unresolved external input")
	}
	r.Tasks[0].Inputs[2].Identity = &agent.EffectiveIdentity{Kind: agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_VERSIONED, Namespace: "vendor.snapshot/v1", Digest: "immutable-revision"}
	if !evaluate(t, r, build)[0].CacheEligible {
		t.Fatal("explicit external identity not reusable")
	}
}

func TestRejectMalformedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*agent.GetEffectiveInputsResponse)
	}{
		{"duplicate task", func(r *agent.GetEffectiveInputsResponse) { r.Tasks = append(r.Tasks, r.Tasks[0]) }},
		{"unrequested", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Task.Phase = agent.TaskPhase_TASK_PHASE_LINT }},
		{"duplicate input", func(r *agent.GetEffectiveInputsResponse) {
			r.Tasks[0].Inputs = append(r.Tasks[0].Inputs, r.Tasks[0].Inputs[0])
		}},
		{"unknown kind", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Kind = 100 }},
		{"invalid UTF-8", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Name = string([]byte{0xff}) }},
		{"bad digest", func(r *agent.GetEffectiveInputsResponse) {
			r.Tasks[0].Inputs[0].Identity.Digest = "secret-should-not-be-echoed"
		}},
		{"sensitive plain hash", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Sensitive = true }},
		{"path escape", func(r *agent.GetEffectiveInputsResponse) {
			r.Tasks[0].Inputs[0].Path = true
			r.Tasks[0].Inputs[0].Name = "../secret-should-not-be-echoed"
		}},
		{"noncanonical path", func(r *agent.GetEffectiveInputsResponse) {
			r.Tasks[0].Inputs[0].Path = true
			r.Tasks[0].Inputs[0].Name = "a/../b"
		}},
		{"unresolved data", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Identity.Kind = 0 }},
		{"unknown algorithm", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Identity.Kind = 99 }},
		{"no mode", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].Inputs[0].Path = true }},
		{"duplicate runtime", func(r *agent.GetEffectiveInputsResponse) { r.Tasks[0].RuntimeServices = []string{"api", "api"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := response(declaration(build))
			tc.mutate(r)
			_, err := Evaluate(r, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "snapshot"}, []Key{build})
			if err == nil {
				t.Fatal("accepted malformed declaration")
			}
			if strings.Contains(err.Error(), "secret-should-not-be-echoed") {
				t.Fatal("leaked input in diagnostic")
			}
		})
	}
}

func TestProtectedInputs(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	a, err := Protect(key, "workspace-key/v1", []byte("shared secret"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Protect(key, "workspace-key/v1", []byte("shared secret"))
	if !proto.Equal(a, b) {
		t.Fatal("unstable identity")
	}
	c, _ := Protect(key, "workspace-key/v2", []byte("shared secret"))
	if a.Digest == c.Digest {
		t.Fatal("rotation not separated")
	}
	d, _ := Protect([]byte(strings.Repeat("x", 32)), "workspace-key/v1", []byte("shared secret"))
	if a.Digest == d.Digest {
		t.Fatal("key not included")
	}
	if _, err := Protect(key, "v1\x00", nil); err == nil {
		t.Fatal("ambiguous namespace accepted")
	}
	if _, err := Protect(nil, "v1", nil); err == nil {
		t.Fatal("weak key accepted")
	}
	in := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_ENVIRONMENT, "token", "")
	in.Sensitive = true
	in.Identity = a
	r := response(declaration(unit, in))
	if !evaluate(t, r, unit)[0].CacheEligible {
		t.Fatal("protected input ineligible")
	}
	wire, _ := proto.Marshal(r)
	if strings.Contains(string(wire), "shared secret") {
		t.Fatal("secret emitted")
	}
}

type wireAgent struct {
	agent.UnimplementedAgentServer
	reply   *agent.GetEffectiveInputsResponse
	failure error
}

func (s *wireAgent) GetEffectiveInputs(context.Context, *agent.GetEffectiveInputsRequest) (*agent.GetEffectiveInputsResponse, error) {
	return s.reply, s.failure
}
func wireClient(t *testing.T, s agent.AgentServer) agent.AgentClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	agent.RegisterAgentServer(server, s)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///agent", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return agent.NewAgentClient(conn)
}
func TestWireVersionConformance(t *testing.T) {
	req := &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "snapshot"}
	info := &agent.AgentInformation{EffectiveInputsVersions: []uint32{Version}}
	for _, tc := range []struct {
		name         string
		server       agent.AgentServer
		info         *agent.AgentInformation
		conservative bool
		code         codes.Code
	}{
		{"new", &wireAgent{reply: response(declaration(build))}, info, false, codes.OK},
		{"old wire server", &agent.UnimplementedAgentServer{}, info, true, codes.OK},
		{"old advertisement", &wireAgent{failure: status.Error(codes.Internal, "must not probe")}, &agent.AgentInformation{}, true, codes.OK},
		{"newer schema", &wireAgent{reply: &agent.GetEffectiveInputsResponse{SchemaVersion: 2}}, info, true, codes.OK},
		{"transport failure", &wireAgent{failure: status.Error(codes.Unavailable, "offline")}, info, false, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Discover(context.Background(), wireClient(t, tc.server), tc.info, req, []Key{build})
			if status.Code(err) != tc.code {
				t.Fatalf("err=%v", err)
			}
			if err == nil && got[0].Conservative != tc.conservative {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestRequiredInventory(t *testing.T) {
	v := &agent.ValidationCapabilities{ArtifactBuild: &agent.ValidationOperationCapability{Supported: true}, Test: &agent.TestValidationCapability{Supported: true, Suites: []*agent.TestSuiteCapability{{Name: "unit"}, {Name: "integration"}}}}
	keys, err := Required(v)
	if err != nil {
		t.Fatal(err)
	}
	got := evaluate(t, response(declaration(build)), keys...)
	if len(got) != 3 || !got[0].Conservative || !got[1].Conservative {
		t.Fatalf("lost required suites: %+v", got)
	}
	if _, err := Required(nil); err == nil {
		t.Fatal("legacy capability accepted without probing")
	}
	v.Test.Suites = nil
	if _, err := Required(v); err == nil {
		t.Fatal("test inventory disappeared")
	}
}

func TestPreviousAdvertisementSchema(t *testing.T) {
	descriptor := protodesc.ToFileDescriptorProto(agent.File_codefly_services_agent_v0_agent_proto)
	for _, message := range descriptor.MessageType {
		if message.GetName() == "AgentInformation" {
			message.Field = message.Field[:len(message.Field)-1]
		}
	}
	descriptor.Service[0].Method = descriptor.Service[0].Method[1:]
	descriptor.Dependency = descriptor.Dependency[:len(descriptor.Dependency)-1]
	oldFile, err := protodesc.NewFile(descriptor, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	original := &agent.AgentInformation{EffectiveInputsVersions: []uint32{1}, Validation: &agent.ValidationCapabilities{ArtifactBuild: &agent.ValidationOperationCapability{Supported: true}}}
	body, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	old := dynamicpb.NewMessage(oldFile.Messages().ByName("AgentInformation"))
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, old); err != nil {
		t.Fatal(err)
	}
	body, err = proto.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip := &agent.AgentInformation{}
	if err := proto.Unmarshal(body, roundtrip); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(roundtrip.Validation, original.Validation) || len(roundtrip.EffectiveInputsVersions) != 0 {
		t.Fatal("additive advertisement broke previous schema")
	}
}

func TestV1IdentityGolden(t *testing.T) {
	// Also produced by Python's deterministic serialization of the v1 schema.
	const want = "sha256:cf974b884f3095d736ba2b81da97774a2d25d19999b2ad039592d6f32cd873a2"
	if got := evaluate(t, response(declaration(build)), build)[0].Identity; got != want {
		t.Fatalf("v1 identity changed: %s", got)
	}
}

func TestResolvedContextIsAuthoritative(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*agent.EffectiveInput)
	}{
		{"stale identity", func(in *agent.EffectiveInput) { in.Identity.Digest = strings.Repeat("b", 64) }},
		{"sensitivity downgrade", func(in *agent.EffectiveInput) { in.Sensitive = false }},
		{"protection downgrade", func(in *agent.EffectiveInput) {
			in.Sensitive = false
			in.Identity.Kind = agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_SHA256
			in.Identity.Namespace = ""
		}},
		{"unresolved context", func(in *agent.EffectiveInput) { in.Identity = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supplied := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_ENVIRONMENT, "MODE", "value")
			supplied.Sensitive = true
			supplied.Identity.Kind = agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_HMAC_SHA256
			supplied.Identity.Namespace = "key/v1"
			declared := proto.Clone(supplied).(*agent.EffectiveInput)
			if tc.name == "unresolved context" {
				tc.change(supplied)
			} else {
				tc.change(declared)
			}
			req := &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "candidate", Context: []*agent.EffectiveInput{supplied}}
			r := response(declaration(unit, declared))
			r.Snapshot = req.Snapshot
			if _, err := Evaluate(r, req, []Key{unit}); err == nil {
				t.Fatal("accepted contradictory context")
			}
			if _, err := Discover(context.Background(), wireClient(t, &wireAgent{reply: r}), &agent.AgentInformation{EffectiveInputsVersions: []uint32{Version}}, req, []Key{unit}); err == nil {
				t.Fatal("discovery accepted contradictory context")
			}
		})
	}
	supplied := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_ENVIRONMENT, "MODE", "value")
	req := &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "snapshot", Context: []*agent.EffectiveInput{supplied, input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_ENVIRONMENT, "unused", "other")}}
	before, err := Evaluate(response(declaration(unit, supplied)), req, []Key{unit})
	if err != nil {
		t.Fatal(err)
	}
	req.Context[1].Identity = nil
	after, err := Evaluate(response(declaration(unit, supplied)), req, []Key{unit})
	if err != nil {
		t.Fatal(err)
	}
	if !after[0].CacheEligible || len(Changed(before, after)) != 0 {
		t.Fatal("unconsumed context invalidated task")
	}
	supplied.Identity.Digest = strings.Repeat("b", 64)
	after, err = Evaluate(response(declaration(unit, supplied)), req, []Key{unit})
	if err != nil {
		t.Fatal(err)
	}
	if len(Changed(before, after)) != 1 {
		t.Fatal("consumed context change was lost")
	}
}

func TestValidatedDeclarationIsRetained(t *testing.T) {
	r := response(declaration(integration,
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SERVICE_IMPLEMENTATION, "api", "implementation"),
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_ARTIFACT, "api/compile/binary", "binary"),
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_VALIDATION, "api/test/unit", "passed"),
	))
	r.Tasks[0].RuntimeServices = []string{"api"}
	req := &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: r.Snapshot}
	for _, complete := range []bool{true, false} {
		r.Tasks[0].Complete = complete
		got, err := Discover(context.Background(), wireClient(t, &wireAgent{reply: r}), &agent.AgentInformation{EffectiveInputsVersions: []uint32{Version}}, req, []Key{integration, unit})
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(got[0].Declaration, r.Tasks[0]) {
			t.Fatal("lost validated inputs or scheduling dependencies")
		}
		if got[1].Declaration != nil {
			t.Fatal("invented declaration for missing suite")
		}
	}
	original := proto.Clone(r.Tasks[0]).(*agent.TaskInputs)
	got, err := Evaluate(r, req, []Key{integration})
	if err != nil {
		t.Fatal(err)
	}
	r.Tasks[0].RuntimeServices[0] = "mutated"
	r.Tasks[0].Inputs[0].Identity.Digest = strings.Repeat("b", 64)
	if !proto.Equal(got[0].Declaration, original) {
		t.Fatal("returned declaration aliases wire response")
	}
	got[0].Declaration.Inputs[1].Name = "changed"
	if r.Tasks[0].Inputs[1].Name == "changed" {
		t.Fatal("wire response aliases returned declaration")
	}
	unknownResponse := response(declaration(integration))
	unknownResponse.SchemaVersion = 2
	fallback := evaluate(t, unknownResponse, integration)
	if fallback[0].Declaration != nil {
		t.Fatal("returned unvalidated future declaration")
	}
}
