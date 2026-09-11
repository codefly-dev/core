// Package ciinputs validates agent-owned effective-input declarations. It does
// not discover language inputs, schedule tasks, or store execution results.
package ciinputs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	agent "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const Version uint32 = 1
const identityDomain = "codefly.effective-inputs/v1\x00"

type Key struct {
	Phase agent.TaskPhase
	Suite string
}

type Task struct {
	Key Key
	// Identity is empty when selection must fall back to the full service and
	// dependency closure. An empty identity must never be used as a cache key.
	Identity      string
	Conservative  bool
	CacheEligible bool
	Reason        string
}

type Client interface {
	GetEffectiveInputs(context.Context, *agent.GetEffectiveInputsRequest, ...grpc.CallOption) (*agent.GetEffectiveInputsResponse, error)
}

// Discover preserves transport failures; only Unimplemented means an old agent.
// Required is the caller's independent operation/suite inventory, never derived
// from the input response. Retain its union across reference and candidate.
func Discover(ctx context.Context, client Client, info *agent.AgentInformation, req *agent.GetEffectiveInputsRequest, required []Key) ([]Task, error) {
	if req == nil || req.SchemaVersion != Version || req.Snapshot == "" {
		return nil, fmt.Errorf("effective inputs require v1 and a snapshot")
	}
	if _, err := validate(&agent.TaskInputs{Inputs: req.Context}); err != nil {
		return nil, err
	}
	if !slices.Contains(info.GetEffectiveInputsVersions(), Version) {
		return Evaluate(nil, req.Snapshot, required)
	}
	response, err := client.GetEffectiveInputs(ctx, req)
	if status.Code(err) == codes.Unimplemented {
		return Evaluate(nil, req.Snapshot, required)
	}
	if err != nil {
		return nil, err
	}
	return Evaluate(response, req.Snapshot, required)
}

// Evaluate validates an untrusted wire declaration and returns every required
// task, including tasks omitted by discovery. Errors contain no input values.
func Evaluate(response *agent.GetEffectiveInputsResponse, snapshot string, required []Key) ([]Task, error) {
	tasks := make([]Task, 0, len(required))
	inventory := map[Key]bool{}
	for _, key := range required {
		if !validKey(key) || inventory[key] {
			return nil, fmt.Errorf("invalid or duplicate required task")
		}
		inventory[key] = true
	}
	declarations := map[Key]*agent.TaskInputs{}
	supported := response != nil && response.SchemaVersion == Version && !unknown(response.ProtoReflect())
	if supported {
		if snapshot == "" || response.Snapshot != snapshot {
			return nil, fmt.Errorf("effective input snapshot mismatch")
		}
		for _, declaration := range response.Tasks {
			key := Key{declaration.GetTask().GetPhase(), declaration.GetTask().GetSuite()}
			if declaration == nil || !validKey(key) || declarations[key] != nil || !inventory[key] {
				return nil, fmt.Errorf("invalid, duplicate or unrequested task declaration")
			}
			declarations[key] = declaration
		}
	}
	for _, key := range required {
		task := Task{Key: key, Conservative: true, Reason: "unsupported declaration"}
		if supported {
			declaration := declarations[key]
			task.Reason = "missing or incomplete declaration"
			if declaration != nil {
				resolved, err := validate(declaration)
				if err != nil {
					return nil, err
				}
				if declaration.Complete && resolved {
					identity, err := fingerprint(declaration)
					if err != nil {
						return nil, err
					}
					task.Identity = identity
					task.Conservative = false
					task.CacheEligible = true
					task.Reason = ""
				} else if !resolved {
					task.Reason = "unresolved effective inputs"
				}
			}
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Key.Phase != tasks[j].Key.Phase {
			return tasks[i].Key.Phase < tasks[j].Key.Phase
		}
		return tasks[i].Key.Suite < tasks[j].Key.Suite
	})
	return tasks, nil
}

func validKey(k Key) bool {
	return k.Phase >= agent.TaskPhase_TASK_PHASE_LINT && k.Phase <= agent.TaskPhase_TASK_PHASE_SOURCE_PACKAGE && ((k.Phase == agent.TaskPhase_TASK_PHASE_TEST) == (k.Suite != ""))
}

func unknown(m protoreflect.Message) bool {
	if len(m.GetUnknown()) != 0 {
		return true
	}
	found := false
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Kind() == protoreflect.MessageKind {
			if f.IsList() {
				l := v.List()
				for i := 0; i < l.Len(); i++ {
					found = found || unknown(l.Get(i).Message())
				}
			} else {
				found = unknown(v.Message())
			}
		}
		return !found
	})
	return found
}

func validate(t *agent.TaskInputs) (bool, error) {
	resolved, plugin, toolchain := true, false, false
	seen := map[string]bool{}
	for _, in := range t.Inputs {
		if in == nil || in.Kind < agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE || in.Kind > agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_EXTERNAL || in.Owner == "" || in.Name == "" {
			return false, fmt.Errorf("invalid effective input")
		}
		key := fmt.Sprintf("%d\x00%s\x00%s", in.Kind, in.Owner, in.Name)
		if strings.ContainsAny(in.Owner+in.Name, "\x00\r\n") || seen[key] {
			return false, fmt.Errorf("invalid or duplicate effective input key")
		}
		seen[key] = true
		if in.Path {
			if path.IsAbs(in.Name) || path.Clean(in.Name) != in.Name || in.Name == "." || in.Name == ".." || strings.HasPrefix(in.Name, "../") || strings.Contains(in.Name, "\\") {
				return false, fmt.Errorf("noncanonical effective input path")
			}
			if in.Mode != 0o100644 && in.Mode != 0o100755 && in.Mode != 0o120000 && in.Mode != 0o160000 {
				return false, fmt.Errorf("invalid effective input mode")
			}
		} else if in.Mode != 0 {
			return false, fmt.Errorf("mode on non-path input")
		}
		id := in.GetIdentity()
		if id.GetKind() == agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_UNSPECIFIED {
			if id.GetDigest() != "" || id.GetNamespace() != "" {
				return false, fmt.Errorf("unresolved identity contains data")
			}
			resolved = false
			continue
		}
		if in.Sensitive && id.Kind != agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_HMAC_SHA256 {
			return false, fmt.Errorf("sensitive input requires protected identity")
		}
		switch id.Kind {
		case agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_SHA256, agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_HMAC_SHA256:
			b, err := hex.DecodeString(id.Digest)
			if err != nil || len(b) != sha256.Size || strings.ToLower(id.Digest) != id.Digest {
				return false, fmt.Errorf("invalid effective input digest")
			}
			if id.Kind == agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_SHA256 && id.Namespace != "" {
				return false, fmt.Errorf("plain hash has namespace")
			}
			if id.Kind == agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_HMAC_SHA256 && id.Namespace == "" {
				return false, fmt.Errorf("protected identity requires key namespace")
			}
		case agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_VERSIONED:
			if id.Namespace == "" || id.Digest == "" {
				return false, fmt.Errorf("external identity requires versioned namespace and digest")
			}
		default:
			return false, fmt.Errorf("unknown effective identity kind")
		}
		plugin = plugin || in.Kind == agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_PLUGIN
		toolchain = toolchain || in.Kind == agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_TOOLCHAIN
	}
	seen = map[string]bool{}
	for _, service := range t.RuntimeServices {
		if service == "" || seen[service] {
			return false, fmt.Errorf("invalid or duplicate runtime service")
		}
		seen[service] = true
	}
	return resolved && plugin && toolchain, nil
}

func fingerprint(t *agent.TaskInputs) (string, error) {
	canonical := proto.Clone(t).(*agent.TaskInputs)
	sort.Slice(canonical.Inputs, func(i, j int) bool {
		a, b := canonical.Inputs[i], canonical.Inputs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Name < b.Name
	})
	sort.Strings(canonical.RuntimeServices)
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("effective inputs cannot be canonically encoded")
	}
	digest := sha256.Sum256(append([]byte(identityDomain), body...))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Changed compares the union of independently required tasks. Missing tasks,
// removed paths and changed dependency edges cannot vanish in the candidate.
func Changed(reference, candidate []Task) []Key {
	before, after := map[Key]Task{}, map[Key]Task{}
	for _, t := range reference {
		before[t.Key] = t
	}
	for _, t := range candidate {
		after[t.Key] = t
	}
	keys := map[Key]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var changed []Key
	for key := range keys {
		a, aok := before[key]
		b, bok := after[key]
		if !aok || !bok || a.Conservative || b.Conservative || a.Identity == "" || b.Identity == "" || a.Identity != b.Identity {
			changed = append(changed, key)
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].Phase != changed[j].Phase {
			return changed[i].Phase < changed[j].Phase
		}
		return changed[i].Suite < changed[j].Suite
	})
	return changed
}

// Protect uses a private, high-entropy key scoped to a reuse trust domain. The
// public namespace identifies key rotation; neither key nor value is emitted.
func Protect(key []byte, namespace string, value []byte) (*agent.EffectiveIdentity, error) {
	if len(key) < 32 || namespace == "" || strings.ContainsRune(namespace, 0) {
		return nil, fmt.Errorf("protected identity requires a 256-bit key and namespace")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(identityDomain))
	mac.Write([]byte(namespace))
	mac.Write([]byte{0})
	mac.Write(value)
	return &agent.EffectiveIdentity{Kind: agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_HMAC_SHA256, Namespace: namespace, Digest: hex.EncodeToString(mac.Sum(nil))}, nil
}

// Required derives task identities from the authoritative capability inventory,
// independently of input discovery. Legacy agents require RPC capability probes
// before callers can supply the inventory to Discover.
func Required(v *agent.ValidationCapabilities) ([]Key, error) {
	if v == nil {
		return nil, fmt.Errorf("legacy validation requires capability probing")
	}
	var keys []Key
	for _, op := range []struct {
		phase      agent.TaskPhase
		capability *agent.ValidationOperationCapability
	}{
		{agent.TaskPhase_TASK_PHASE_LINT, v.Lint},
		{agent.TaskPhase_TASK_PHASE_COMPILE, v.Compile},
		{agent.TaskPhase_TASK_PHASE_AUDIT, v.Audit},
		{agent.TaskPhase_TASK_PHASE_SBOM, v.Sbom},
		{agent.TaskPhase_TASK_PHASE_ARTIFACT_BUILD, v.ArtifactBuild},
		{agent.TaskPhase_TASK_PHASE_SYNC, v.Sync},
		{agent.TaskPhase_TASK_PHASE_SOURCE_PACKAGE, v.SourcePackage},
	} {
		if op.capability.GetSupported() {
			keys = append(keys, Key{Phase: op.phase})
		}
	}
	if v.Test.GetSupported() {
		if len(v.Test.Suites) == 0 {
			return nil, fmt.Errorf("supported tests require a suite inventory")
		}
		seen := map[string]bool{}
		for _, suite := range v.Test.Suites {
			name := suite.GetName()
			if name == "" || seen[name] {
				return nil, fmt.Errorf("invalid or duplicate advertised suite")
			}
			seen[name] = true
			keys = append(keys, Key{Phase: agent.TaskPhase_TASK_PHASE_TEST, Suite: name})
		}
	}
	return keys, nil
}
