package architecture

import (
	"fmt"
	"strings"

	"github.com/codefly-dev/core/graph"
)

// ToGraph converts a DAG (e.g. service or module dependencies) into the generic graph.Graph
// so it can be merged with LSP-derived graphs for cross-repo, cross-language views.
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
		out.AddEdge(e.From, e.To, graph.EdgeDependsOn)
	}
	return out
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
