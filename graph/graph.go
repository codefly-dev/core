package graph

import (
	"fmt"
	"sort"
	"strings"
)

// NodeKind is the type of a node in the graph (cross-repo, cross-language).
const (
	KindRepo    = "repo"
	KindModule  = "module"
	KindService = "service"
	KindFile    = "file"
	KindSymbol  = "symbol"
	KindEndpoint = "endpoint"
)

// EdgeKind is the type of an edge (dependency, containment, call, etc.).
const (
	EdgeDependsOn = "depends_on"
	EdgeContains  = "contains"
	EdgeCalls     = "calls"
	EdgeReferences = "references"
	EdgeChildOf   = "child_of"
)

// Node is a vertex in the graph with a stable ID, kind, and optional attributes.
// IDs should be unique and stable across repos/languages (e.g. "repo/module/service", "repo/module/service/file.go", "repo/module/service/file.go#SymbolName").
type Node struct {
	ID    string
	Kind  string
	Attrs map[string]any
}

// Edge is a directed edge with an optional kind for cross-repo/cross-language graphs.
type Edge struct {
	From string
	To   string
	Kind string
}

// Graph is a generic directed graph of nodes and edges for dependency trees and LSP-derived relationships.
// Use it for service dependencies, module graphs, symbol trees, and call/reference edges.
type Graph struct {
	name   string
	nodes  map[string]*Node
	edges  map[string][]Edge // from ID -> list of edges (each has To)
	incoming map[string]int   // count of edges into each node (for topo sort)
}

// New creates a new empty graph.
func New(name string) *Graph {
	return &Graph{
		name:    name,
		nodes:   make(map[string]*Node),
		edges:   make(map[string][]Edge),
		incoming: make(map[string]int),
	}
}

// Name returns the graph name.
func (g *Graph) Name() string { return g.name }

// AddNode adds or updates a node. Returns the node for chaining.
func (g *Graph) AddNode(id, kind string) *Node {
	if g.nodes[id] == nil {
		g.nodes[id] = &Node{ID: id, Kind: kind, Attrs: make(map[string]any)}
	} else {
		g.nodes[id].Kind = kind
	}
	return g.nodes[id]
}

// SetAttr sets an attribute on a node (creates node if missing).
func (g *Graph) SetAttr(id, kind, key string, value any) {
	n := g.AddNode(id, kind)
	if n.Attrs == nil {
		n.Attrs = make(map[string]any)
	}
	n.Attrs[key] = value
}

// AddEdge adds a directed edge. Creates nodes if they do not exist (with empty kind).
func (g *Graph) AddEdge(from, to, kind string) {
	if kind == "" {
		kind = EdgeDependsOn
	}
	if g.nodes[from] == nil {
		g.nodes[from] = &Node{ID: from, Kind: "", Attrs: make(map[string]any)}
	}
	if g.nodes[to] == nil {
		g.nodes[to] = &Node{ID: to, Kind: "", Attrs: make(map[string]any)}
	}
	edge := Edge{From: from, To: to, Kind: kind}
	exists := false
	for _, e := range g.edges[from] {
		if e.To == to && e.Kind == kind {
			exists = true
			break
		}
	}
	if !exists {
		g.edges[from] = append(g.edges[from], edge)
		g.incoming[to]++
	}
}

// HasNode returns true if the node exists.
func (g *Graph) HasNode(id string) bool { return g.nodes[id] != nil }

// Node returns the node by ID or nil.
func (g *Graph) Node(id string) *Node { return g.nodes[id] }

// Nodes returns all nodes (order not specified).
func (g *Graph) Nodes() []*Node {
	out := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	return out
}

// Edges returns all edges.
func (g *Graph) Edges() []Edge {
	var out []Edge
	for _, list := range g.edges {
		out = append(out, list...)
	}
	return out
}

// OutEdges returns edges from the given node.
func (g *Graph) OutEdges(from string) []Edge { return g.edges[from] }

// InEdges returns edges into the given node.
func (g *Graph) InEdges(to string) []Edge {
	var out []Edge
	for from, list := range g.edges {
		for _, e := range list {
			if e.To == to {
				out = append(out, Edge{From: from, To: to, Kind: e.Kind})
			}
		}
	}
	return out
}

// Children returns node IDs that are targets of edges from id.
func (g *Graph) Children(id string) []string {
	var out []string
	for _, e := range g.edges[id] {
		out = append(out, e.To)
	}
	return out
}

// Parents returns node IDs that have edges into id.
func (g *Graph) Parents(id string) []string {
	var out []string
	for from, list := range g.edges {
		for _, e := range list {
			if e.To == id {
				out = append(out, from)
				break
			}
		}
	}
	return out
}

// Merge merges another graph into g. Node IDs must be unique; same ID overwrites (kind/attrs).
func (g *Graph) Merge(other *Graph) {
	for _, n := range other.nodes {
		existing := g.nodes[n.ID]
		if existing != nil {
			existing.Kind = n.Kind
			for k, v := range n.Attrs {
				if existing.Attrs == nil {
					existing.Attrs = make(map[string]any)
				}
				existing.Attrs[k] = v
			}
		} else {
			attrs := make(map[string]any)
			for k, v := range n.Attrs {
				attrs[k] = v
			}
			g.nodes[n.ID] = &Node{ID: n.ID, Kind: n.Kind, Attrs: attrs}
		}
	}
	for _, e := range other.Edges() {
		g.AddEdge(e.From, e.To, e.Kind)
	}
}

// SubgraphFrom returns a new graph containing all nodes reachable from startID (following edges).
func (g *Graph) SubgraphFrom(startID string) (*Graph, error) {
	if !g.HasNode(startID) {
		return nil, fmt.Errorf("node %q not found", startID)
	}
	out := New(g.name + "-from-" + startID)
	visited := make(map[string]bool)
	var dfs func(id string)
	dfs = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		n := g.Node(id)
		out.AddNode(n.ID, n.Kind)
		for k, v := range n.Attrs {
			out.SetAttr(n.ID, n.Kind, k, v)
		}
		for _, e := range g.edges[id] {
			dfs(e.To)
			out.AddEdge(e.From, e.To, e.Kind)
		}
	}
	dfs(startID)
	return out, nil
}

// TopologicalSort returns node IDs in topological order (no cycle check; use on DAGs).
func (g *Graph) TopologicalSort() ([]string, error) {
	inc := make(map[string]int)
	for id, c := range g.incoming {
		inc[id] = c
	}
	var queue []string
	for id := range g.nodes {
		if inc[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, e := range g.edges[id] {
			inc[e.To]--
			if inc[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if len(order) != len(g.nodes) {
		return nil, fmt.Errorf("graph has a cycle")
	}
	return order, nil
}

// ReachableFrom returns true if toID is reachable from fromID.
func (g *Graph) ReachableFrom(fromID, toID string) bool {
	sub, err := g.SubgraphFrom(fromID)
	if err != nil {
		return false
	}
	return sub.HasNode(toID)
}

// Print returns a human-readable list of edges.
func (g *Graph) Print(verb string) string {
	if verb == "" {
		verb = "->"
	}
	var lines []string
	for _, n := range g.Nodes() {
		kids := g.Children(n.ID)
		if len(kids) == 0 {
			lines = append(lines, nodeString(n))
			continue
		}
		sort.Strings(kids)
		lines = append(lines, fmt.Sprintf("%s %s %s", nodeString(n), verb, strings.Join(kids, ", ")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func nodeString(n *Node) string {
	if n.Kind != "" {
		return fmt.Sprintf("%s (%s)", n.ID, n.Kind)
	}
	return n.ID
}

// NodeID builds a stable cross-repo node ID from components (e.g. repo/module/service, or repo/module/service/file.go#Symbol).
func NodeID(parts ...string) string {
	return strings.Join(parts, "/")
}

// SymbolID returns a node ID for a symbol in a file within a scope (repo/module/service/file#name).
func SymbolID(scopeRepo, scopeModule, scopeService, file, symbolName string) string {
	return NodeID(scopeRepo, scopeModule, scopeService, file+"#"+symbolName)
}

// FileID returns a node ID for a file within a scope.
func FileID(scopeRepo, scopeModule, scopeService, file string) string {
	return NodeID(scopeRepo, scopeModule, scopeService, file)
}
