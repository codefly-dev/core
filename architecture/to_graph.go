package architecture

import (
	"fmt"
	"strings"

	"github.com/codefly-dev/core/graph"
	"github.com/codefly-dev/core/resources"
)

// ToGraph converts a DAG (e.g. service or module dependencies) into the generic graph.Graph.
func ToGraph(dag *DAG, name string) *graph.Graph {
	if name == "" {
		name = dag.Name
	}
	out := graph.New(name)
	for _, n := range dag.Nodes() {
		kind := nodeKind(n.Type)
		out.AddNode(n.ID, kind)
	}
	for _, e := range dag.Edges() {
		kinds := dag.EdgeKinds(e.From, e.To)
		if len(kinds) == 0 {
			out.AddEdge(e.From, e.To, graph.EdgeDependsOn)
			continue
		}
		for _, kind := range kinds {
			out.AddEdge(e.From, e.To, edgeKind(kind))
		}
	}
	return out
}

// edgeKind maps a dependency kind onto the generic graph vocabulary. The legacy
// (undeclared) kind stays depends_on so consumers of the untyped graph are
// unaffected.
func edgeKind(kind resources.DependencyKind) string {
	if kind == resources.DependencyKindLegacy {
		return graph.EdgeDependsOn
	}
	return string(kind)
}

func nodeKind(t any) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case string:
		return strings.ToLower(v)
	default:
		return fmt.Sprint(v)
	}
}
