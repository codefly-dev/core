package graph

import (
	"testing"
)

func TestGraph_Empty(t *testing.T) {
	g := New("empty")
	if len(g.Nodes()) != 0 || len(g.Edges()) != 0 {
		t.Fatalf("empty graph: nodes=%d edges=%d", len(g.Nodes()), len(g.Edges()))
	}
	order, err := g.TopologicalSort()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("empty topo order: %v", order)
	}
}

func TestGraph_AddNodeAndEdge(t *testing.T) {
	g := New("test")
	g.AddNode("a", KindService)
	g.AddNode("b", KindService)
	g.AddEdge("a", "b", EdgeDependsOn)
	if !g.HasNode("a") || !g.HasNode("b") {
		t.Fatal("nodes not found")
	}
	edges := g.OutEdges("a")
	if len(edges) != 1 || edges[0].To != "b" || edges[0].Kind != EdgeDependsOn {
		t.Fatalf("unexpected edges: %+v", edges)
	}
}

func TestGraph_Merge(t *testing.T) {
	a := New("a")
	a.AddNode("x", KindModule)
	a.AddEdge("x", "y", EdgeDependsOn)
	b := New("b")
	b.AddNode("y", KindService)
	b.AddNode("z", KindFile)
	b.AddEdge("y", "z", EdgeContains)
	a.Merge(b)
	if !a.HasNode("z") {
		t.Fatal("merged node z not found")
	}
	if len(a.Nodes()) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(a.Nodes()))
	}
}

func TestGraph_SubgraphFrom(t *testing.T) {
	g := New("test")
	g.AddNode("1", KindService)
	g.AddNode("2", KindService)
	g.AddNode("3", KindService)
	g.AddEdge("1", "2", EdgeDependsOn)
	g.AddEdge("2", "3", EdgeDependsOn)
	sub, err := g.SubgraphFrom("1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Nodes()) != 3 {
		t.Fatalf("subgraph expected 3 nodes, got %d", len(sub.Nodes()))
	}
}

func TestGraph_TopologicalSort(t *testing.T) {
	g := New("dag")
	g.AddEdge("a", "b", EdgeDependsOn)
	g.AddEdge("b", "c", EdgeDependsOn)
	g.AddEdge("a", "c", EdgeDependsOn)
	order, err := g.TopologicalSort()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 nodes in order, got %d", len(order))
	}
	pos := make(map[string]int)
	for i, id := range order {
		pos[id] = i
	}
	if pos["a"] >= pos["b"] || pos["b"] >= pos["c"] {
		t.Errorf("invalid topo order: %v", order)
	}
}

func TestNodeID(t *testing.T) {
	id := NodeID("repo", "mod", "svc", "pkg/file.go")
	if id != "repo/mod/svc/pkg/file.go" {
		t.Errorf("NodeID: got %q", id)
	}
	symID := SymbolID("r", "m", "s", "f.go", "Foo")
	if symID != "r/m/s/f.go#Foo" {
		t.Errorf("SymbolID: got %q", symID)
	}
	fileID := FileID("r", "m", "s", "pkg/handlers.go")
	if fileID != "r/m/s/pkg/handlers.go" {
		t.Errorf("FileID: got %q", fileID)
	}
}

func TestGraph_ReachableFrom(t *testing.T) {
	g := New("reach")
	g.AddEdge("a", "b", EdgeDependsOn)
	g.AddEdge("b", "c", EdgeDependsOn)
	g.AddEdge("a", "d", EdgeDependsOn)
	if !g.ReachableFrom("a", "c") {
		t.Error("a should reach c")
	}
	if !g.ReachableFrom("a", "d") {
		t.Error("a should reach d")
	}
	if g.ReachableFrom("d", "a") {
		t.Error("d should not reach a")
	}
	if g.ReachableFrom("a", "missing") {
		t.Error("should not reach missing node")
	}
}

func TestGraph_ParentsAndInEdges(t *testing.T) {
	g := New("parents")
	g.AddNode("x", KindService)
	g.AddNode("y", KindService)
	g.AddNode("z", KindService)
	g.AddEdge("x", "z", EdgeDependsOn)
	g.AddEdge("y", "z", EdgeDependsOn)
	parents := g.Parents("z")
	if len(parents) != 2 {
		t.Fatalf("expected 2 parents of z, got %d", len(parents))
	}
	in := g.InEdges("z")
	if len(in) != 2 {
		t.Fatalf("expected 2 in-edges to z, got %d", len(in))
	}
}

func TestGraph_DuplicateEdgeIdempotent(t *testing.T) {
	g := New("dup")
	g.AddEdge("a", "b", EdgeDependsOn)
	g.AddEdge("a", "b", EdgeDependsOn)
	edges := g.OutEdges("a")
	if len(edges) != 1 {
		t.Fatalf("duplicate edge should be idempotent: got %d edges", len(edges))
	}
}

func TestGraph_TopologicalSortCycle(t *testing.T) {
	g := New("cycle")
	g.AddEdge("a", "b", EdgeDependsOn)
	g.AddEdge("b", "c", EdgeDependsOn)
	g.AddEdge("c", "a", EdgeDependsOn)
	_, err := g.TopologicalSort()
	if err == nil {
		t.Fatal("expected error on cycle")
	}
}

func TestGraph_SetAttr(t *testing.T) {
	g := New("attrs")
	g.SetAttr("n", KindSymbol, "name", "Foo")
	g.SetAttr("n", KindSymbol, "line", 42)
	n := g.Node("n")
	if n == nil || n.Kind != KindSymbol {
		t.Fatalf("node: %+v", n)
	}
	if n.Attrs["name"] != "Foo" || n.Attrs["line"] != 42 {
		t.Fatalf("attrs: %+v", n.Attrs)
	}
}

func TestGraph_Print(t *testing.T) {
	g := New("print")
	g.AddEdge("a", "b", EdgeDependsOn)
	s := g.Print("requires")
	if s == "" || len(s) < 4 {
		t.Fatalf("Print: %q", s)
	}
}
