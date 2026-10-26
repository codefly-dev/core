package architecture_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/resources"
)

// unboundWorkspace is a module's own workspace whose records service requires
// an interface no unit in it implements, declared with kind.
func unboundWorkspace(t *testing.T, kind string) *resources.Workspace {
	t.Helper()
	root := t.TempDir()
	agent := "agent:\n    kind: runtime::service\n    name: go-grpc\n    version: 0.0.1\n    publisher: codefly.ai\n"
	files := map[string]string{
		"workspace.codefly.yaml":                   "name: record-store\nlayout: modules\nmodules:\n    - name: record-store\n",
		"modules/record-store/module.codefly.yaml": "kind: module\nname: record-store\nservices:\n    - name: records\n    - name: store\n",
		"modules/record-store/services/records/service.codefly.yaml": "kind: service\nname: records\nversion: 0.0.0\n" + agent +
			"endpoints:\n    - name: grpc\n      api: grpc\nservice-dependencies:\n    - name: store\n    - interface: codefly.dev/authority@0.1.0\n      kind: " + kind + "\n",
		"modules/record-store/services/store/service.codefly.yaml": "kind: service\nname: store\nversion: 0.0.0\n" + agent +
			"endpoints:\n    - name: tcp\n      api: tcp\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	workspace, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	require.NoError(t, err)
	return workspace
}

// An external interface nothing in the workspace implements is no node and no
// edge: running the module's service starts its own dependencies only.
func TestUnboundExternalInterfaceIsNoEdge(t *testing.T) {
	ctx := context.Background()
	workspace := unboundWorkspace(t, "external")

	graph, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"record-store/records", "record-store/store"}, uniques(graph.Services()))
	order, err := graph.OrderTo(ctx, "record-store/records")
	require.NoError(t, err)
	require.Equal(t, []string{"record-store/store"}, uniques(order), "the target's dependencies, the target excluded")

	closure, err := architecture.SelectClosure(ctx, workspace, "record-store/records")
	require.NoError(t, err)
	require.NoError(t, closure.Verify(ctx, resources.PhaseRun))
	order, err = closure.Order(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"record-store/store", "record-store/records"}, uniques(order))
}

// An unbound interface whose kind starts its provider is a composition that
// cannot run its consumer: the graph and the closure both refuse it, naming
// the interface.
func TestUnboundRuntimeInterfaceRefusesTheComposition(t *testing.T) {
	ctx := context.Background()
	workspace := unboundWorkspace(t, "runtime")

	_, err := architecture.NewServiceDependencies(ctx, workspace)
	require.ErrorContains(t, err, "record-store/records requires codefly.dev/authority@0.1.0, which no unit in the workspace implements")

	closure, err := architecture.SelectClosure(ctx, workspace, "record-store/records")
	require.NoError(t, err)
	err = closure.Verify(ctx, resources.PhaseRun)
	require.ErrorContains(t, err, "codefly.dev/authority@0.1.0 (required by record-store/records)")
	_, err = closure.Order(ctx)
	require.ErrorContains(t, err, "which no unit in the workspace implements")
}
