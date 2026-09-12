package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

const withRunnables = "testdata/workspaces/with-runnables"

func TestLoadRunnable(t *testing.T) {
	ctx := context.Background()

	r, err := resources.LoadRunnableFromDir(ctx, filepath.Join(withRunnables, "runnables/word-count"))
	require.NoError(t, err)
	require.Equal(t, "word-count", r.Name)
	require.Equal(t, resources.RunnableKind, r.Kind)
	require.Equal(t, "0.1.0", r.Version)
	require.True(t, r.Agent.IsRunnable())
	require.Equal(t, "python", r.Agent.Name)

	require.Equal(t, resources.RunnableProtocolV1, r.Contract.Protocol)
	require.Len(t, r.Contract.Input.Fields, 2)
	options := r.Contract.Input.Fields[1]
	require.Equal(t, resources.RunnableFieldObject, options.Type)
	require.True(t, options.Optional)
	require.False(t, options.Nullable)
	stopWords := options.Fields[1]
	require.Equal(t, resources.RunnableFieldArray, stopWords.Type)
	require.True(t, stopWords.Nullable)
	require.Equal(t, resources.RunnableFieldString, stopWords.Items.Type)
	require.Len(t, r.Contract.Output.Fields, 2)

	require.Equal(t, "handler.py", r.Entrypoint.Handler)
	require.Equal(t, []string{"pyproject.toml", "uv.lock"}, r.Entrypoint.Inputs)
	require.FileExists(t, r.HandlerPath())

	require.Equal(t, []resources.RunnableFacility{resources.RunnableFacilityNative, resources.RunnableFacilityKubernetes}, r.Execution.Facilities)
	require.Equal(t, 2*time.Minute, r.Execution.GetTimeout())
	require.Equal(t, resources.RunnableCancellationSignal, r.Execution.Cancellation)
	require.Equal(t, resources.RunnableRecoveryRecompute, r.Execution.Recovery)
	require.Equal(t, uint64(65536), r.Execution.MaxInputBytes())
	require.Equal(t, resources.DefaultRunnablePayloadBytes, r.Execution.MaxOutputBytes())

	require.Len(t, r.ServiceDependencies, 1)
	require.Equal(t, resources.DependencyKindRuntime, r.ServiceDependencies[0].Kind)
	require.Equal(t, "3.12", r.Spec["python"])
}

func TestWorkspaceComposesServicesAndRunnables(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, withRunnables)
	require.NoError(t, err)
	require.Len(t, workspace.Services, 1)
	require.Len(t, workspace.Runnables, 1)

	mod, err := workspace.LoadModuleFromName(ctx, "with-runnables")
	require.NoError(t, err)
	require.Len(t, mod.ServiceReferences, 1)
	require.Len(t, mod.RunnableReferences, 1)
	require.Equal(t, "with-runnables", mod.RunnableReferences[0].Module)

	svc, err := mod.LoadServiceFromName(ctx, "store")
	require.NoError(t, err)
	require.Equal(t, "store", svc.Name)

	r, err := mod.LoadRunnableFromName(ctx, "word-count")
	require.NoError(t, err)
	require.Equal(t, "with-runnables", r.Module())
	require.Equal(t, "with-runnables/word-count", r.Unique())
	require.Equal(t, "with-runnables/word-count", r.Identity().Unique())
	require.Equal(t, "0.1.0", r.Identity().Version)

	runnables, err := mod.LoadRunnables(ctx)
	require.NoError(t, err)
	require.Len(t, runnables, 1)

	all, err := workspace.LoadAllRunnables(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	byName, err := workspace.FindRunnableByName(ctx, "word-count")
	require.NoError(t, err)
	require.Equal(t, r.Unique(), byName.Unique())
	byUnique, err := workspace.FindRunnableByName(ctx, "with-runnables/word-count")
	require.NoError(t, err)
	require.Equal(t, r.Unique(), byUnique.Unique())
	_, err = workspace.FindRunnableByName(ctx, "missing")
	require.Error(t, err)

	refs, err := resources.DiscoverRunnables(ctx, workspace.Dir())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "word-count", refs[0].Name)
}

func TestRunnableProtoRoundTripsContract(t *testing.T) {
	ctx := context.Background()

	r, err := resources.LoadRunnableFromDir(ctx, filepath.Join(withRunnables, "runnables/word-count"))
	require.NoError(t, err)
	r.SetModule("with-runnables")

	proto, err := r.Proto(ctx)
	require.NoError(t, err)
	require.Equal(t, basev0.Agent_RUNNABLE, proto.GetAgent().GetKind())
	require.Equal(t, "handler.py", proto.GetHandler())
	require.Equal(t, []string{"pyproject.toml", "uv.lock"}, proto.GetBuildInputs())
	require.Len(t, proto.GetServiceDependencies(), 1)
	require.Equal(t, "with-runnables", proto.GetServiceDependencies()[0].GetModule())
	require.Equal(t, "runtime", proto.GetServiceDependencies()[0].GetKind())
	require.Equal(t, []string{"tcp"}, proto.GetServiceDependencies()[0].GetEndpoints())

	execution := proto.GetExecution()
	require.Len(t, execution.GetFacilities(), 2)
	require.Equal(t, basev0.RunnableFacility_NATIVE, execution.GetFacilities()[0].GetKind())
	require.Equal(t, 2*time.Minute, execution.GetTimeout().AsDuration())
	require.Equal(t, basev0.RunnableExecution_CANCELLATION_SIGNAL, execution.GetCancellation())
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, execution.GetRecovery())
	require.Equal(t, uint64(65536), execution.GetMaxInputBytes())
	require.Equal(t, resources.DefaultRunnablePayloadBytes, execution.GetMaxOutputBytes())

	contract := proto.GetContract()
	require.Equal(t, basev0.RunnableField_OBJECT, contract.GetInput().GetFields()[1].GetType())
	require.Equal(t, basev0.RunnableField_STRING, contract.GetInput().GetFields()[1].GetFields()[1].GetItems().GetType())

	back, err := resources.RunnableContractFromProto(contract)
	require.NoError(t, err)
	require.Equal(t, r.Contract, back)

	// A wire contract is held to the same profile as a YAML one.
	contract.GetOutput().GetFields()[0].Type = basev0.RunnableField_UNKNOWN
	_, err = resources.RunnableContractFromProto(contract)
	require.ErrorContains(t, err, "outside the bounded profile")
}

func TestRunnableSaveRoundTrip(t *testing.T) {
	ctx := context.Background()

	r, err := resources.LoadRunnableFromDir(ctx, filepath.Join(withRunnables, "runnables/word-count"))
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, r.SaveToDir(ctx, dir))
	first, err := os.ReadFile(filepath.Join(dir, resources.RunnableConfigurationName))
	require.NoError(t, err)

	reloaded, err := resources.LoadRunnableFromDir(ctx, dir)
	require.NoError(t, err)
	reloaded.WithDir(r.Dir())
	require.Equal(t, r, reloaded)

	again := t.TempDir()
	require.NoError(t, reloaded.SaveToDir(ctx, again))
	second, err := os.ReadFile(filepath.Join(again, resources.RunnableConfigurationName))
	require.NoError(t, err)
	require.Equal(t, string(first), string(second), "canonical serialization must be stable across round-trips")

	require.ErrorContains(t, r.SaveToDir(ctx, ""), "directory is empty")
}

func TestProtoRequiresModuleForModulelessDependency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join(withRunnables, "runnables/word-count", resources.RunnableConfigurationName))
	require.NoError(t, err)
	declaration := map[string]any{}
	require.NoError(t, yaml.Unmarshal(source, &declaration))
	delete(declaration["service-dependencies"].([]any)[0].(map[string]any), "module")
	content, err := yaml.Marshal(declaration)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.RunnableConfigurationName), content, 0o600))

	r, err := resources.LoadRunnableFromDir(ctx, dir)
	require.NoError(t, err)
	_, err = r.Proto(ctx)
	require.ErrorContains(t, err, `dependency "store" declares no module`)

	r.SetModule("with-runnables")
	proto, err := r.Proto(ctx)
	require.NoError(t, err)
	require.Equal(t, "with-runnables", proto.GetServiceDependencies()[0].GetModule())
}

func TestNewRunnableIsCompleteOrNothing(t *testing.T) {
	ctx := context.Background()
	agent := &resources.Agent{Kind: resources.RunnableAgent, Name: "python", Version: "0.0.1", Publisher: "codefly.dev"}

	r, err := resources.NewRunnable(ctx, "fresh", agent, "handler.py")
	require.NoError(t, err)
	require.Equal(t, resources.RunnableKind, r.Kind)
	require.Equal(t, resources.RunnableProtocolV1, r.Contract.Protocol)
	require.NoError(t, r.SaveToDir(ctx, t.TempDir()))

	_, err = resources.NewRunnable(ctx, "fresh", nil, "handler.py")
	require.ErrorContains(t, err, "declares no agent")
	_, err = resources.NewRunnable(ctx, "fresh", agent, "")
	require.ErrorContains(t, err, "handler is required")
	for _, name := range []string{"", "../escape", "nested/runnable", `nested\runnable`, ".", ".."} {
		_, err := resources.NewRunnable(ctx, name, agent, "handler.py")
		require.Error(t, err, name)
	}
}

func TestLoadRunnableRejectsUnknownKeys(t *testing.T) {
	ctx := context.Background()
	source, err := os.ReadFile(filepath.Join(withRunnables, "runnables/word-count", resources.RunnableConfigurationName))
	require.NoError(t, err)
	for _, tc := range []struct{ name, from, to, want string }{
		{"misspelled modifier", "optional: true", "optionnal: true", "field optionnal not found"},
		{"misspelled section", "cancellation: signal", "cancelation: signal", "field cancelation not found"},
		{"stray top-level key", "kind: runnable", "kind: runnable\nretries: 3", "field retries not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			content := strings.Replace(string(source), tc.from, tc.to, 1)
			require.NotEqual(t, string(source), content)
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.RunnableConfigurationName), []byte(content), 0o600))
			_, err := resources.LoadRunnableFromDir(ctx, dir)
			require.ErrorContains(t, err, tc.want)
		})
	}
	// Agent-specific keys under spec stay free-form.
	dir := t.TempDir()
	content := strings.Replace(string(source), `python: "3.12"`, "python: \"3.12\"\n  extras: [tls]", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.RunnableConfigurationName), []byte(content), 0o600))
	r, err := resources.LoadRunnableFromDir(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, []any{"tls"}, r.Spec["extras"])
}

func TestModuleNewRunnableInFlatWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(withRunnables)))
	agent := &resources.Agent{Kind: resources.RunnableAgent, Name: "python", Version: "0.0.1", Publisher: "codefly.dev"}

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "with-runnables")
	require.NoError(t, err)

	r, err := mod.NewRunnable(ctx, "echo", agent, "handler.py")
	require.NoError(t, err)
	require.Equal(t, "with-runnables", r.Module())
	require.FileExists(t, filepath.Join(r.Dir(), resources.RunnableConfigurationName))
	_, err = mod.NewRunnable(ctx, "echo", agent, "handler.py")
	require.ErrorContains(t, err, "already exists")

	reloaded, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.Len(t, reloaded.Services, 1, "adding a runnable must not disturb services")
	require.Len(t, reloaded.Runnables, 2)
	echo, err := reloaded.FindRunnableByName(ctx, "echo")
	require.NoError(t, err)
	require.Empty(t, echo.Contract.Input.Fields)
	all, err := reloaded.LoadAllRunnables(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestModuleNewRunnableRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(withRunnables)))
	agent := &resources.Agent{Kind: resources.RunnableAgent, Name: "python", Version: "0.0.1", Publisher: "codefly.dev"}
	before, err := os.ReadFile(filepath.Join(dir, resources.WorkspaceConfigurationName))
	require.NoError(t, err)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "with-runnables")
	require.NoError(t, err)

	// An unsaveable declaration never touches disk or the module.
	_, err = mod.NewRunnable(ctx, "broken", &resources.Agent{Kind: resources.ServiceAgent, Name: "go", Version: "1.0.0", Publisher: "codefly.dev"}, "handler.py")
	require.ErrorContains(t, err, "is not \"codefly:runnable\"")
	require.NoDirExists(t, filepath.Join(dir, "runnables/broken"))
	require.False(t, mod.ExistsRunnable("broken"))

	// A directory that already exists without a reference is not adopted.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "runnables/stale"), 0o755))
	_, err = mod.NewRunnable(ctx, "stale", agent, "handler.py")
	require.ErrorContains(t, err, "already exists without a module reference")
	require.False(t, mod.ExistsRunnable("stale"))

	after, err := os.ReadFile(filepath.Join(dir, resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a failed creation must not persist a reference")
	reloaded, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	all, err := reloaded.LoadAllRunnables(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestRunnableModuleLayoutWithPathOverride(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "modules/ops/work/counter"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte("name: layered\nlayout: modules\nmodules:\n  - name: ops\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "modules/ops", resources.ModuleConfigurationName), []byte("kind: module\nname: ops\nservices: []\nrunnables:\n  - name: counter\n    path: work/counter\n"), 0o600))
	source, err := os.ReadFile(filepath.Join(withRunnables, "runnables/word-count", resources.RunnableConfigurationName))
	require.NoError(t, err)
	declaration := map[string]any{}
	require.NoError(t, yaml.Unmarshal(source, &declaration))
	declaration["name"] = "counter"
	delete(declaration, "service-dependencies")
	content, err := yaml.Marshal(declaration)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "modules/ops/work/counter", resources.RunnableConfigurationName), content, 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	r, err := workspace.LoadRunnableFromUnique(ctx, "ops/counter")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "modules/ops/work/counter"), r.Dir())

	mod, err := workspace.LoadModuleFromName(ctx, "ops")
	require.NoError(t, err)
	require.NoError(t, mod.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(dir, "modules/ops", resources.ModuleConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(saved), "path: work/counter")
	require.NotContains(t, string(saved), "module:")

	// A reference whose declaration names something else is a misfiled runnable.
	_, err = mod.LoadRunnableFromReference(ctx, &resources.RunnableReference{Name: "renamed", PathOverride: resources.OverridePath("renamed", "work/counter")})
	require.ErrorContains(t, err, "declares name <counter>")
}

func TestRunnableValidationRejectsIncompleteContracts(t *testing.T) {
	ctx := context.Background()
	load := func(t *testing.T, mutate func(declaration map[string]any)) error {
		t.Helper()
		source, err := os.ReadFile(filepath.Join(withRunnables, "runnables/word-count", resources.RunnableConfigurationName))
		require.NoError(t, err)
		declaration := map[string]any{}
		require.NoError(t, yaml.Unmarshal(source, &declaration))
		mutate(declaration)
		content, err := yaml.Marshal(declaration)
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, resources.RunnableConfigurationName), content, 0o600))
		_, err = resources.LoadRunnableFromDir(ctx, dir)
		return err
	}
	contract := func(d map[string]any) map[string]any { return d["contract"].(map[string]any) }
	execution := func(d map[string]any) map[string]any { return d["execution"].(map[string]any) }
	inputFields := func(d map[string]any) []any { return contract(d)["input"].(map[string]any)["fields"].([]any) }

	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"wrong kind", func(d map[string]any) { d["kind"] = "job" }, `has kind "job"`},
		{"escaping name", func(d map[string]any) { d["name"] = "../up" }, "single path component"},
		{"loose version", func(d map[string]any) { d["version"] = "1.0" }, "not a strict semantic version"},
		{"latest version", func(d map[string]any) { d["version"] = "latest" }, "not a strict semantic version"},
		{"no agent", func(d map[string]any) { delete(d, "agent") }, "declares no agent"},
		{"service agent", func(d map[string]any) { d["agent"].(map[string]any)["kind"] = "codefly:service" }, "is not \"codefly:runnable\""},
		{"unpinned agent", func(d map[string]any) { d["agent"].(map[string]any)["version"] = "latest" }, "must pin its agent version"},
		{"no contract", func(d map[string]any) { delete(d, "contract") }, "contract is required"},
		{"unknown protocol", func(d map[string]any) { contract(d)["protocol"] = "codefly.runnable/v2" }, `protocol "codefly.runnable/v2" is not supported`},
		{"no output", func(d map[string]any) { delete(contract(d), "output") }, "output schema is required"},
		{"enum type", func(d map[string]any) { inputFields(d)[0].(map[string]any)["type"] = "enum" }, "outside the bounded profile"},
		{"bad field name", func(d map[string]any) { inputFields(d)[0].(map[string]any)["name"] = "full text" }, "must be an identifier"},
		{"duplicate field", func(d map[string]any) { inputFields(d)[1].(map[string]any)["name"] = "text" }, "declared twice"},
		{"fields on string", func(d map[string]any) {
			inputFields(d)[0].(map[string]any)["fields"] = []any{map[string]any{"name": "x", "type": "string"}}
		}, "declares fields but is not an object"},
		{"array without items", func(d map[string]any) {
			delete(inputFields(d)[1].(map[string]any)["fields"].([]any)[1].(map[string]any), "items")
		}, "array without items"},
		{"named items", func(d map[string]any) {
			inputFields(d)[1].(map[string]any)["fields"].([]any)[1].(map[string]any)["items"].(map[string]any)["name"] = "word"
		}, "items must not be named"},
		{"no handler", func(d map[string]any) { delete(d["entrypoint"].(map[string]any), "handler") }, "handler is required"},
		{"escaping handler", func(d map[string]any) { d["entrypoint"].(map[string]any)["handler"] = "../handler.py" }, "must stay within the resource directory"},
		{"escaping input", func(d map[string]any) { d["entrypoint"].(map[string]any)["inputs"] = []any{"/etc/passwd"} }, "must stay within the resource directory"},
		{"no execution", func(d map[string]any) { delete(d, "execution") }, "execution is required"},
		{"no facilities", func(d map[string]any) { execution(d)["facilities"] = []any{} }, "at least one facility"},
		{"unknown facility", func(d map[string]any) { execution(d)["facilities"] = []any{"lambda"} }, `facility "lambda" is not supported`},
		{"no timeout", func(d map[string]any) { delete(execution(d), "timeout") }, "timeout is required"},
		{"bad timeout", func(d map[string]any) { execution(d)["timeout"] = "soon" }, "not a duration"},
		{"zero timeout", func(d map[string]any) { execution(d)["timeout"] = "0s" }, "must be positive"},
		{"no cancellation", func(d map[string]any) { delete(execution(d), "cancellation") }, `cancellation "" is not supported`},
		{"no recovery", func(d map[string]any) { delete(execution(d), "recovery") }, `recovery "" is not supported`},
		{"retry policy", func(d map[string]any) { execution(d)["recovery"] = "retry" }, `recovery "retry" is not supported`},
		{"completion with endpoints", func(d map[string]any) {
			d["service-dependencies"].([]any)[0].(map[string]any)["kind"] = "completion"
		}, "completion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := load(t, tc.mutate)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestExistingWorkspacesIgnoreRunnables(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range []string{"testdata/workspaces/with-jobs", "testdata/workspaces/with-library", "testdata/workspaces/module-layout"} {
		workspace, err := resources.LoadWorkspaceFromDir(ctx, fixture)
		require.NoError(t, err, fixture)
		require.Empty(t, workspace.Runnables, fixture)
		all, err := workspace.LoadAllRunnables(ctx)
		require.NoError(t, err, fixture)
		require.Empty(t, all, fixture)
	}
}
