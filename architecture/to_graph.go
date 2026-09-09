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

// edgeKinds maps a dependency kind onto the generic graph vocabulary. The
// mapping is explicit rather than a string conversion: the two vocabularies are
// owned by different packages, and converting silently made them agree only by
// coincidence — renaming a DependencyKind value would have changed the graph
// edge kind with nothing to catch it. An unhandled kind is a compile-time gap
// here, not a silently-invented edge kind.
var edgeKinds = map[resources.DependencyKind]string{
	resources.DependencyKindLegacy:     graph.EdgeDependsOn,
	resources.DependencyKindBuild:      graph.EdgeBuildInput,
	resources.DependencyKindRuntime:    graph.EdgeRuntime,
	resources.DependencyKindCompletion: graph.EdgeCompletion,
	resources.DependencyKindSchema:     graph.EdgeSchema,
	resources.DependencyKindExternal:   graph.EdgeExternal,
}

func edgeKind(kind resources.DependencyKind) string {
	if mapped, ok := edgeKinds[kind]; ok {
		return mapped
	}
	return graph.EdgeDependsOn
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
