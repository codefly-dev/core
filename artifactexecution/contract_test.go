package artifactexecution

import (
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func requestFixture() *basev0.ArtifactExecution {
	digest := "sha256:" + strings.Repeat("a", 64)
	return &basev0.ArtifactExecution{ContractVersion: Contract, SelectionIdentity: digest, Target: "modules/app", Service: "api", Protocol: BuilderRender, ExecutorDigest: digest, ConfigurationIdentity: "hmac-" + digest, BindingIdentity: digest,
		Inputs:  []*basev0.ArtifactExecutionInput{{Name: "runtime", Target: "modules/app", Artifact: "runtime", Uri: "https://artifacts.example.test/runtime", MediaType: "application/octet-stream", Digest: digest}},
		Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", MediaType: "application/yaml"}}}
}

func TestExecutionIdentityMatchesPython(t *testing.T) {
	request, err := Prepare(requestFixture())
	require.NoError(t, err)
	require.Equal(t, "sha256:2f4abd015ad1cdee13c688e50b06e7f55b0fbf7f155e7bdd886bd71e7d8a039d", request.Identity)
}

func TestPrepareCanonicalizesAndBindsEveryInput(t *testing.T) {
	raw := requestFixture()
	extra := proto.Clone(raw.Inputs[0]).(*basev0.ArtifactExecutionInput)
	extra.Name = "contract"
	raw.Inputs = append(raw.Inputs, extra)
	first, err := Prepare(raw)
	require.NoError(t, err)
	require.Empty(t, raw.Identity)
	raw.Inputs[0], raw.Inputs[1] = raw.Inputs[1], raw.Inputs[0]
	second, err := Prepare(raw)
	require.NoError(t, err)
	require.True(t, proto.Equal(first, second))
	for _, scenario := range []string{"bytes", "target", "media", "uri", "executor", "configuration", "bindings", "service"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := proto.Clone(first).(*basev0.ArtifactExecution)
			other := "sha256:" + strings.Repeat("b", 64)
			switch scenario {
			case "bytes":
				candidate.Inputs[0].Digest = other
			case "target":
				candidate.Target = "modules/other"
			case "media":
				candidate.Inputs[0].MediaType = "application/json"
			case "uri":
				candidate.Inputs[0].Uri = "https://other.example.test/runtime"
			case "executor":
				candidate.ExecutorDigest = other
			case "configuration":
				candidate.ConfigurationIdentity = "hmac-" + other
			case "bindings":
				candidate.BindingIdentity = other
			case "service":
				candidate.Service = "other"
			}
			_, err := Prepare(candidate)
			require.ErrorContains(t, err, "identity does not match")
			candidate.Identity = ""
			changed, err := Prepare(candidate)
			require.NoError(t, err)
			require.NotEqual(t, first.Identity, changed.Identity)
		})
	}
}

func TestPrepareRefusesUnsupportedOrUnsafeBindings(t *testing.T) {
	for _, scenario := range []string{"future contract", "future protocol", "unknown fields", "input unknown fields", "duplicate input", "duplicate output", "missing input", "missing output", "credential URI", "file render", "insecure URI", "traversal target", "empty media", "noncanonical media", "secret digest", "output path in request"} {
		t.Run(scenario, func(t *testing.T) {
			raw := requestFixture()
			switch scenario {
			case "future contract":
				raw.ContractVersion = "artifact-execution/v2"
			case "future protocol":
				raw.Protocol = "other/v1"
			case "unknown fields":
				raw.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
			case "input unknown fields":
				raw.Inputs[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
			case "duplicate input":
				raw.Inputs = append(raw.Inputs, raw.Inputs[0])
			case "duplicate output":
				raw.Outputs = append(raw.Outputs, raw.Outputs[0])
			case "missing input":
				raw.Inputs = nil
			case "missing output":
				raw.Outputs = nil
			case "credential URI":
				raw.Inputs[0].Uri = "https://user:password@example.test/image"
			case "file render":
				raw.Inputs[0].Uri = "file:///private/checkout"
			case "insecure URI":
				raw.Inputs[0].Uri = "http://example.test/image"
			case "traversal target":
				raw.Target = "modules/../outside"
			case "empty media":
				raw.Inputs[0].MediaType = ""
			case "noncanonical media":
				raw.Inputs[0].MediaType = "Application/JSON"
			case "secret digest":
				raw.ConfigurationIdentity = raw.SelectionIdentity
			case "output path in request":
				raw.Outputs[0].Path = "preselected.yaml"
			}
			_, err := Prepare(raw)
			require.Error(t, err)
		})
	}
}

func TestReceiptCannotChangeOutputLocationsOrContract(t *testing.T) {
	request, err := Prepare(requestFixture())
	require.NoError(t, err)
	for _, path := range []string{"../outside", "/absolute", "a/../../outside", `..\outside`, "", ".", "a/../b", "a//b", "file:outside"} {
		receipt := &basev0.ArtifactExecutionReceipt{Identity: request.Identity, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", MediaType: "application/yaml", Digest: request.SelectionIdentity, Path: path}}}
		require.Error(t, CheckReceipt(request, receipt), path)
	}
	require.Error(t, Check(request, BuilderRender, request.ExecutorDigest, nil))
	require.Error(t, Check(request, BuilderBuild, request.ExecutorDigest, []string{Contract}))
	require.Error(t, Check(request, BuilderRender, "sha256:"+strings.Repeat("b", 64), []string{Contract}))
	require.NoError(t, Check(request, BuilderRender, request.ExecutorDigest, []string{Contract}))
}
