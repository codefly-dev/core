package architecture

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/graph"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestToGraph_SimpleDAG(t *testing.T) {
	dag := NewDAG("test")
	dag.AddNode("mod/a").WithType(resources.MODULE)
	dag.AddNode("mod/a/svc1").WithType(resources.SERVICE)
	dag.AddNode("mod/a/svc2").WithType(resources.SERVICE)
	dag.AddEdge("mod/a", "mod/a/svc1")
	dag.AddEdge("mod/a", "mod/a/svc2")
	dag.AddEdge("mod/a/svc1", "mod/a/svc2")

	g := ToGraph(dag, "converted")
	require.NotNil(t, g)
	require.Equal(t, 3, len(g.Nodes()))
	require.Equal(t, 3, len(g.Edges()))

	n := g.Node("mod/a")
	require.NotNil(t, n)
	require.Equal(t, graph.KindModule, n.Kind)
	n = g.Node("mod/a/svc1")
	require.NotNil(t, n)
	require.Equal(t, graph.KindService, n.Kind)

	order, err := g.TopologicalSort()
	require.NoError(t, err)
	require.Equal(t, 3, len(order))
	pos := make(map[string]int)
	for i, id := range order {
		pos[id] = i
	}
	require.True(t, pos["mod/a"] < pos["mod/a/svc1"], "module before svc1")
	require.True(t, pos["mod/a/svc1"] < pos["mod/a/svc2"], "svc1 before svc2")
}

func TestToGraph_Empty(t *testing.T) {
	dag := NewDAG("empty")
	g := ToGraph(dag, "")
	require.NotNil(t, g)
	require.Equal(t, 0, len(g.Nodes()))
	require.Equal(t, 0, len(g.Edges()))
}

func TestServiceDependencies_Graph(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	dep, err := NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.NotNil(t, dep)

	g := dep.Graph()
	require.NotNil(t, g)
	require.Equal(t, 4, len(g.Nodes()), "organization, accounts, gateway, frontend")
	require.Equal(t, 4, len(g.Edges()))

	// All nodes should be kind service
	for _, n := range g.Nodes() {
		require.Equal(t, graph.KindService, n.Kind, "node %q", n.ID)
	}

	// All edges should be depends_on
	for _, e := range g.Edges() {
		require.Equal(t, graph.EdgeDependsOn, e.Kind, "edge %s -> %s", e.From, e.To)
	}

	// Use actual unique IDs from dep (e.g. "module/gateway", "module/frontend")
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway"))
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "frontend"))
	require.True(t, g.ReachableFrom(gateway.MustUnique(), frontend.MustUnique()), "gateway should reach frontend")
}