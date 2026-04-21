package code

import (
	"testing"
)

func buildTestGraph() *CodeGraph {
	g := NewCodeGraph()

	g.AddNode(&CodeNode{ID: "main.go::SetupRoutes", Kind: NodeFunction, Name: "SetupRoutes", File: "main.go", Line: 10})
	g.AddNode(&CodeNode{ID: "main.go::main", Kind: NodeFunction, Name: "main", File: "main.go", Line: 1})
	g.AddNode(&CodeNode{ID: "handler.go::HandleUser", Kind: NodeFunction, Name: "HandleUser", File: "handler.go", Line: 5})
	g.AddNode(&CodeNode{ID: "handler.go::HandleAdmin", Kind: NodeFunction, Name: "HandleAdmin", File: "handler.go", Line: 20})
	g.AddNode(&CodeNode{ID: "types.go::User", Kind: NodeType, Name: "User", File: "types.go", Line: 1})
	g.AddNode(&CodeNode{ID: "service.go::handleUser", Kind: NodeMethod, Name: "handleUser", File: "service.go", Line: 15})

	// main calls SetupRoutes and HandleUser
	g.AddEdge(Edge{From: "main.go::main", To: "main.go::SetupRoutes", Kind: EdgeCalls})
	g.AddEdge(Edge{From: "main.go::main", To: "handler.go::HandleUser", Kind: EdgeCalls})
	// SetupRoutes calls HandleUser and HandleAdmin
	g.AddEdge(Edge{From: "main.go::SetupRoutes", To: "handler.go::HandleUser", Kind: EdgeCalls})
	g.AddEdge(Edge{From: "main.go::SetupRoutes", To: "handler.go::HandleAdmin", Kind: EdgeCalls})

	return g
}

func TestFindDefinitions_Exact(t *testing.T) {
	g := buildTestGraph()

	defs := g.FindDefinitions("SetupRoutes")
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].ID != "main.go::SetupRoutes" {
		t.Errorf("expected main.go::SetupRoutes, got %s", defs[0].ID)
	}
}

func TestFindDefinitions_CaseInsensitive(t *testing.T) {
	g := buildTestGraph()

	defs := g.FindDefinitions("setuproutes")
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition (case-insensitive), got %d", len(defs))
	}
}

func TestFindDefinitions_MultipleMatches(t *testing.T) {
	g := buildTestGraph()

	// Both HandleUser (function) and handleUser (method) should match
	defs := g.FindDefinitions("handleuser")
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions for 'handleuser', got %d", len(defs))
	}
}

func TestFindDefinitions_NotFound(t *testing.T) {
	g := buildTestGraph()

	defs := g.FindDefinitions("NonExistent")
	if len(defs) != 0 {
		t.Errorf("expected 0 definitions, got %d", len(defs))
	}
}

func TestFindDefinitionsByKind(t *testing.T) {
	g := buildTestGraph()

	fns := g.FindDefinitionsByKind("handleuser", NodeFunction)
	if len(fns) != 1 {
		t.Fatalf("expected 1 function, got %d", len(fns))
	}
	if fns[0].Kind != NodeFunction {
		t.Errorf("expected NodeFunction, got %v", fns[0].Kind)
	}

	methods := g.FindDefinitionsByKind("handleuser", NodeMethod)
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}

	types := g.FindDefinitionsByKind("User", NodeType)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
}

func TestFindUsages(t *testing.T) {
	g := buildTestGraph()

	// HandleUser is called by main and SetupRoutes
	usages := g.FindUsages("HandleUser")
	if len(usages) != 2 {
		t.Fatalf("expected 2 usages of HandleUser, got %d", len(usages))
	}

	ids := make(map[string]bool)
	for _, u := range usages {
		ids[u.ID] = true
	}
	if !ids["main.go::main"] {
		t.Error("expected main to be a caller of HandleUser")
	}
	if !ids["main.go::SetupRoutes"] {
		t.Error("expected SetupRoutes to be a caller of HandleUser")
	}
}

func TestFindUsages_NoCalls(t *testing.T) {
	g := buildTestGraph()

	// main is not called by anyone
	usages := g.FindUsages("main")
	if len(usages) != 0 {
		t.Errorf("expected 0 usages of main, got %d", len(usages))
	}
}

func TestSearchSymbols(t *testing.T) {
	g := buildTestGraph()

	// "handle" should match HandleUser, HandleAdmin, handleUser
	results := g.SearchSymbols("handle")
	if len(results) != 3 {
		t.Fatalf("expected 3 results for 'handle', got %d", len(results))
	}
}

func TestSearchSymbols_NoMatch(t *testing.T) {
	g := buildTestGraph()

	results := g.SearchSymbols("zzzzz")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestNameIndex_RemoveNode(t *testing.T) {
	g := buildTestGraph()

	// Verify HandleUser is found
	defs := g.FindDefinitions("HandleUser")
	if len(defs) != 2 {
		t.Fatalf("expected 2 before removal, got %d", len(defs))
	}

	// Remove one
	g.RemoveNode("handler.go::HandleUser")

	defs = g.FindDefinitions("HandleUser")
	if len(defs) != 1 {
		t.Fatalf("expected 1 after removal, got %d", len(defs))
	}
	if defs[0].ID != "service.go::handleUser" {
		t.Errorf("expected service.go::handleUser to remain, got %s", defs[0].ID)
	}
}

func TestNameIndex_RebuildIndexes(t *testing.T) {
	g := buildTestGraph()

	// Clear and rebuild
	g.nameIdx = make(map[string][]string)
	if g.NameIndexSize() != 0 {
		t.Fatal("expected empty nameIdx after clear")
	}

	g.RebuildIndexes()

	if g.NameIndexSize() == 0 {
		t.Fatal("expected non-empty nameIdx after rebuild")
	}

	defs := g.FindDefinitions("SetupRoutes")
	if len(defs) != 1 {
		t.Errorf("expected 1 definition after rebuild, got %d", len(defs))
	}
}

func TestNameIndexSize(t *testing.T) {
	g := buildTestGraph()

	// Names: setuproutes, main, handleuser (x2 same key), handleadmin, user
	// = 5 unique lowercase keys
	if got := g.NameIndexSize(); got != 5 {
		t.Errorf("expected 5 unique names, got %d", got)
	}
}
