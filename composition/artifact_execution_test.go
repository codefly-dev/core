package composition

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
)

func (f *nestedFixture) executedInputs(t *testing.T, resolved *ResolvedComposition) DeploymentInputs {
	t.Helper()
	inputs := runtimeInputs()
	batch, err := f.registry.engine.PrepareArtifactExecutions(t.Context(), resolved, "render", runtimeInputs())
	require.NoError(t, err)
	for _, prepared := range batch {
		target := prepared.Request().Target
		content := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + strings.TrimPrefix(target, "modules/") + "\n"
		receipt := &basev0.ArtifactExecutionReceipt{Identity: prepared.Request().Identity, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", Path: "manifests.yaml", MediaType: "application/yaml", Digest: fixtureArtifact("manifests", ArtifactRuntime, content).Digest}}}
		verified, err := VerifyArtifactExecution(t.Context(), prepared, receipt, []ExecutionOutputInput{{Name: "manifests", Content: strings.NewReader(content)}})
		require.NoError(t, err)
		inputs.Executions = append(inputs.Executions, verified)
	}
	return inputs
}

func TestBatchExecutionConsumesRuntimeStreamsOnce(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.NoError(t, err)
	batch, err := f.registry.engine.PrepareArtifactExecutions(t.Context(), resolved, "render", runtimeInputs())
	require.NoError(t, err)
	require.Len(t, batch, 2)
	require.NotEqual(t, batch[0].Request().Identity, batch[1].Request().Identity)
	for _, execution := range batch {
		request := execution.Request()
		one, err := f.registry.engine.PrepareArtifactExecution(t.Context(), resolved, request.Target, request.Service, "render", runtimeInputs())
		require.NoError(t, err)
		require.Equal(t, one.Request(), request)
	}
}

func TestArtifactExecutionRequiresExactReceiptsAndActualBytes(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.NoError(t, err)
	prepared, err := f.registry.engine.PrepareArtifactExecution(t.Context(), resolved, "modules/saas", "store", "render", runtimeInputs())
	require.NoError(t, err)
	request := prepared.Request()
	require.Equal(t, "modules/saas", request.Inputs[0].Target)
	require.Equal(t, artifactexecution.BuilderRender, request.Protocol)
	request.Inputs[0].Digest = "modified"
	require.NotEqual(t, request, prepared.Request(), "callers cannot mutate prepared evidence")
	for _, scenario := range []string{"valid", "missing acknowledgement", "wrong identity", "wrong bytes", "missing bytes", "extra output", "wrong media", "duplicate output"} {
		t.Run(scenario, func(t *testing.T) {
			receipt := &basev0.ArtifactExecutionReceipt{Identity: prepared.Request().Identity, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", Path: "manifests.yaml", MediaType: "application/yaml", Digest: fixtureArtifact("out", ArtifactRuntime, "rendered manifests").Digest}}}
			outputs := []ExecutionOutputInput{{Name: "manifests", Content: strings.NewReader("rendered manifests")}}
			switch scenario {
			case "missing acknowledgement":
				receipt = nil
			case "wrong identity":
				receipt.Identity = f.root.Digest
			case "wrong bytes":
				outputs[0].Content = strings.NewReader("other manifests")
			case "missing bytes":
				outputs = nil
			case "extra output":
				outputs = append(outputs, ExecutionOutputInput{Name: "unselected", Content: strings.NewReader("unselected")})
			case "wrong media":
				receipt.Outputs[0].MediaType = "application/json"
			case "duplicate output":
				receipt.Outputs = append(receipt.Outputs, receipt.Outputs[0])
			}
			_, err := VerifyArtifactExecution(t.Context(), prepared, receipt, outputs)
			if scenario == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDeploymentQualificationBindsRenderedOutputs(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.NoError(t, err)
	inputs := f.executedInputs(t, resolved)
	record, err := f.registry.engine.CheckDeploymentInputs(t.Context(), resolved, inputs)
	require.NoError(t, err)
	now := time.Now()
	q := Qualification{Schema: "codefly/deployment-qualification/v1", SelectionIdentity: record.SelectionIdentity, RuntimeIdentity: record.RuntimeIdentity, BindingIdentity: record.BindingIdentity, ExecutionIdentity: record.ExecutionIdentity, Kind: "functional", Signer: "reviewer", ExpiresAt: now.Add(time.Hour)}
	data, err := json.Marshal(q)
	require.NoError(t, err)
	policy := DeploymentPolicy{RequiredQualifications: []string{"functional"}, QualificationSigners: map[string]map[string]ed25519.PublicKey{"functional": {"reviewer": f.registry.key.Public().(ed25519.PublicKey)}}}
	for _, scenario := range []string{"valid", "missing render", "duplicate instance", "different output", "different target", "different selection", "record mutation"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := f.executedInputs(t, resolved)
			candidate.Qualifications = []SignedQualification{{Statement: data, Signature: ed25519.Sign(f.registry.key, data)}}
			switch scenario {
			case "missing render":
				candidate.Executions = nil
			case "duplicate instance":
				candidate.Executions[1] = candidate.Executions[0]
			case "different output":
				prepared, err := f.registry.engine.PrepareArtifactExecution(t.Context(), resolved, "modules/saas", "store", "render", runtimeInputs())
				require.NoError(t, err)
				receipt := &basev0.ArtifactExecutionReceipt{Identity: prepared.Request().Identity, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", Path: "manifests.yaml", MediaType: "application/yaml", Digest: fixtureArtifact("out", ArtifactRuntime, "changed").Digest}}}
				index := slices.IndexFunc(candidate.Executions, func(execution *VerifiedArtifactExecution) bool { return execution.record.Target == "modules/saas" })
				candidate.Executions[index], err = VerifyArtifactExecution(t.Context(), prepared, receipt, []ExecutionOutputInput{{Name: "manifests", Content: strings.NewReader("changed")}})
				require.NoError(t, err)
			case "different target":
				candidate.Bindings["environment"] = f.root.Digest
			case "different selection":
				options := f.options
				options.ConfigurationIdentity, err = ConfigurationIdentity(bytes.Repeat([]byte{7}, 32), map[string]string{"changed": "configuration"})
				require.NoError(t, err)
				other, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, options)
				require.NoError(t, err)
				candidate.Executions = f.executedInputs(t, other).Executions
			case "record mutation":
				check := runtimeInputs()
				check.Executions = candidate.Executions
				view, err := f.registry.engine.CheckDeploymentInputs(t.Context(), resolved, check)
				require.NoError(t, err)
				view.Executions[0].Outputs[0].Digest = "mutated"
			}
			_, err := f.registry.engine.AdmitDeployment(t.Context(), resolved, candidate, policy, now)
			if scenario == "valid" || scenario == "record mutation" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				if scenario == "different output" {
					require.ErrorContains(t, err, "qualification")
				}
			}
		})
	}
}

func TestExecutionFilesAreConfinedAndHashed(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.NoError(t, err)
	prepared, err := f.registry.engine.PrepareArtifactExecution(t.Context(), resolved, "modules/saas", "store", "render", runtimeInputs())
	require.NoError(t, err)
	for _, scenario := range []string{"valid", "changed bytes", "missing file", "extra file", "directory", "symlink escape", "traversal"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "manifests.yaml")
			receipt := &basev0.ArtifactExecutionReceipt{Identity: prepared.Request().Identity, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "manifests", Path: "manifests.yaml", MediaType: "application/yaml", Digest: fixtureArtifact("out", ArtifactRuntime, "rendered").Digest}}}
			switch scenario {
			case "valid":
				writeFile(t, path, "rendered")
			case "changed bytes":
				writeFile(t, path, "changed")
			case "extra file":
				writeFile(t, path, "rendered")
				writeFile(t, filepath.Join(root, "unapproved.yaml"), "unapproved workload")
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "symlink escape":
				outside := filepath.Join(t.TempDir(), "outside.yaml")
				writeFile(t, outside, "rendered")
				require.NoError(t, os.Symlink(outside, path))
			case "traversal":
				receipt.Outputs[0].Path = "../outside.yaml"
			}
			_, err := VerifyArtifactExecutionDirectory(t.Context(), prepared, receipt, root)
			if scenario == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestExecutionMappingsCannotInferOrAcquireMissingInputs(t *testing.T) {
	for _, scenario := range []string{"missing mapping", "missing artifact", "unused target", "source render", "missing media", "omitted runtime", "wrong build media", "pending derived input"} {
		t.Run(scenario, func(t *testing.T) {
			f := nestedSelectionFixture(t, "build")
			manifest := f.moduleManifest
			manifest.Version = "4.6.0"
			operation := &manifest.Services[0].ArtifactOperations[0]
			want := ""
			switch scenario {
			case "missing mapping":
				manifest.Services[0].ArtifactOperations = nil
			case "missing artifact":
				operation.Inputs["runtime"] = ArtifactReference{Name: "unpublished"}
				want = "not published"
			case "unused target":
				operation.Inputs["runtime"] = ArtifactReference{Target: "modules/unused", Name: "store"}
				want = "nonparticipating"
			case "source render":
				operation.Inputs["runtime"] = ArtifactReference{Name: "source"}
				want = "cannot acquire implementation source"
			case "missing media":
				manifest.ReleaseArtifacts[0].MediaType = ""
				want = "media type"
			case "omitted runtime":
				operation.Inputs["runtime"] = ArtifactReference{Name: "sdk"}
				want = "omits a selected service runtime"
			case "wrong build media":
				f.options.SourceBuilds = []string{"modules/saas"}
				manifest.Services[0].ArtifactOperations[1].Outputs["store"] = "application/json"
				want = "output media type"
			case "pending derived input":
				f.options.SourceBuilds = []string{"modules/saas"}
				manifest.Services[0].ArtifactOperations[1].Inputs["runtime"] = ArtifactReference{Name: "store"}
				want = "unresolved derived output"
			}
			candidate := f.registry.publish(t, manifest)
			f.descriptor.Replacements = []Replacement{{Target: "modules/saas", Release: candidate, Rationale: "new declarations"}}
			resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
			if want != "" {
				require.ErrorContains(t, err, want)
				return
			}
			require.NoError(t, err)
			_, err = f.registry.engine.PrepareArtifactExecution(t.Context(), resolved, "modules/saas", "store", "render", runtimeInputs())
			require.ErrorContains(t, err, "artifact operation is missing")
			_, err = f.registry.engine.AdmitDeployment(t.Context(), resolved, runtimeInputs(), DeploymentPolicy{}, time.Now())
			require.ErrorContains(t, err, "artifact operation is missing")
		})
	}
}
