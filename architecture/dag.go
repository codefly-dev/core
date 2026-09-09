package architecture

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	observabilityv0 "github.com/codefly-dev/core/generated/go/codefly/observability/v0"
	"github.com/codefly-dev/core/resources"
)

type DAG struct {
	Name          string
	nodes         map[string]bool
	edges         map[string][]string
	incomingEdges map[string]int

	nodeTypes map[string]any
	edgeKinds map[Edge][]resources.DependencyKind
	verb      string
}

func NewDAG(name string) *DAG {
	return &DAG{
		Name:          name,
		nodes:         make(map[string]bool),
		edges:         make(map[string][]string),
		incomingEdges: make(map[string]int),
		nodeTypes:     make(map[string]any),
		edgeKinds:     make(map[Edge][]resources.DependencyKind),
	}
}

func (g *DAG) Verb() string {
	if g.verb == "" {
		return "->"
	}
	return g.verb
}

type WrappedNode struct {
	u string
	g *DAG
}

func (w *WrappedNode) WithType(t any) *WrappedNode {
	w.g.nodeTypes[w.u] = t
	return w
}

func (w *WrappedNode) WithTypeOf(node string) {
	if t, ok := w.g.nodeTypes[node]; ok {
		w.g.nodeTypes[w.u] = t
	}
}

func (g *DAG) AddNode(u string) *WrappedNode {
	g.nodes[u] = true
	return &WrappedNode{
		u: u,
		g: g,
	}
}

func (g *DAG) AddEdge(u, v string) {
	if !g.nodes[u] {
		g.nodes[u] = true
	}
	if !g.nodes[v] {
		g.nodes[v] = true
	}
	if !slices.Contains(g.edges[u], v) {
		g.edges[u] = append(g.edges[u], v)
		g.incomingEdges[v]++
	}
}

// AddKindedEdge adds an edge and records the dependency kind it carries. An
// edge may carry several kinds when the same pair is declared more than once.
func (g *DAG) AddKindedEdge(u, v string, kind resources.DependencyKind) {
	g.AddEdge(u, v)
	edge := Edge{From: u, To: v}
	if !slices.Contains(g.edgeKinds[edge], kind) {
		g.edgeKinds[edge] = append(g.edgeKinds[edge], kind)
	}
}

// EdgeKinds returns the dependency kinds carried by an edge. An edge added
// through AddEdge carries none; see edgeKindsOrLegacy for how those are treated
// when selecting a stage. The result is a copy so a caller cannot reach into
// the graph's own state.
func (g *DAG) EdgeKinds(from, to string) []resources.DependencyKind {
	return slices.Clone(g.edgeKinds[Edge{From: from, To: to}])
}

// edgeKindsOrLegacy treats an edge recorded without any kind as legacy rather
// than as participating in nothing. AddEdge is the untyped constructor (module
// graphs use it exclusively), and an untyped edge must mean "constrains every
// stage" — the same thing an undeclared kind means in YAML. Letting it mean
// "constrains no stage" made stage selection silently return an edgeless graph.
func (g *DAG) edgeKindsOrLegacy(edge Edge) []resources.DependencyKind {
	if kinds := g.edgeKinds[edge]; len(kinds) > 0 {
		return kinds
	}
	return []resources.DependencyKind{resources.DependencyKindLegacy}
}

// inheritEdgeKinds copies the kinds src records for an edge onto the same edge
// here, so a derived graph keeps knowing which stage each edge constrains.
func (g *DAG) inheritEdgeKinds(src *DAG, edge Edge) {
	if kinds := src.edgeKinds[edge]; len(kinds) > 0 {
		g.edgeKinds[edge] = slices.Clone(kinds)
	}
}

// ForStage returns a graph holding every node but only the edges whose kinds
// constrain the given stage. Nodes are kept so a service is still resolvable
// in a stage that imposes no ordering on it.
//
// The unit here is a STAGE, not a phase: only an elementary stage is a sortable
// graph. Merging the stages of a composite phase produces a false cycle between
// a build-time and a run-time edge that point opposite ways.
//
// An unknown stage is an error rather than an empty result: dropping every edge
// is indistinguishable from "nothing depends on anything", so a typo in a stage
// name would silently remove all ordering instead of failing.
func (g *DAG) ForStage(stage resources.Stage) (*DAG, error) {
	if err := stage.Validate(); err != nil {
		return nil, err
	}
	out := NewDAG(fmt.Sprintf("%s-%s", g.Name, stage))
	out.verb = g.verb
	for _, node := range g.Nodes() {
		out.AddNode(node.ID).WithType(node.Type)
	}
	for _, edge := range g.Edges() {
		for _, kind := range g.edgeKindsOrLegacy(edge) {
			if kind.Participates(stage) {
				out.AddKindedEdge(edge.From, edge.To, kind)
			}
		}
	}
	return out, nil
}

type Node struct {
	ID   string
	Type any
}

func (n Node) String() string {
	if n.Type == nil {
		return n.ID
	}
	return fmt.Sprintf("%s (%s)", n.ID, n.Type)
}

func (g *DAG) Node(node string) *Node {
	if !g.HasNode(node) {
		return nil
	}
	return &Node{
		ID:   node,
		Type: g.nodeTypes[node],
	}
}

func (g *DAG) Nodes() []Node {
	var nodes []Node
	for node := range g.nodes {
		n := g.Node(node)
		if n != nil {
			nodes = append(nodes, *n)
		}
	}
	return nodes
}

type Edge struct {
	From string
	To   string
}

func (g *DAG) EdgeString(edge Edge) string {
	return fmt.Sprintf("(%s %s %s)", g.Node(edge.From), g.Verb(), g.Node(edge.To))
}

func (g *DAG) Edges() []Edge {
	var edges []Edge
	for from, tos := range g.edges {
		for _, to := range tos {
			edges = append(edges, Edge{
				From: from,
				To:   to,
			})
		}
	}
	return edges
}

func (g *DAG) NodeFrom(node string) []Node {
	var nodes []Node
	for _, to := range g.edges[node] {
		n := g.Node(to)
		if n != nil {
			nodes = append(nodes, *n)
		}
	}
	return nodes
}

func (g *DAG) WithRelationVerb(verb string) {
	g.verb = verb
}

func (g *DAG) Print() string {
	var res []string
	for _, node := range g.Nodes() {
		// Sort the edges for consistent output
		var elements []string
		for _, to := range g.NodeFrom(node.ID) {
			elements = append(elements, to.String())
		}
		if len(elements) == 0 {
			res = append(res, node.String())
			continue
		}
		sort.Strings(elements)
		res = append(res, fmt.Sprintf("%s %s %s", node, g.Verb(), strings.Join(elements, ", ")))
	}
	return strings.Join(res, "\n")
}

func (g *DAG) PrintAsDot() string {
	var builder strings.Builder
	builder.WriteString("digraph {\n")
	for _, edge := range g.Edges() {
		builder.WriteString(fmt.Sprintf("\t\"%s\" -> \"%s\";\n", edge.From, edge.To))
	}
	builder.WriteString("}\n")
	return builder.String()
}

func (g *DAG) Invert() *DAG {
	inverted := NewDAG(g.Name + "-inverted")
	for _, node := range g.Nodes() {
		inverted.AddNode(node.ID).WithType(g.nodeTypes[node.ID])
	}
	for _, edge := range g.Edges() {
		inverted.AddEdge(edge.To, edge.From)
		if kinds := g.edgeKinds[edge]; len(kinds) > 0 {
			inverted.edgeKinds[Edge{From: edge.To, To: edge.From}] = slices.Clone(kinds)
		}
	}
	return inverted
}

func ToType(t any) observabilityv0.GraphNode_Type {
	// Nodes added via AddEdge without WithType carry a nil/non-string type;
	// an unchecked t.(string) panicked the observability graph endpoint.
	s, ok := t.(string)
	if !ok {
		return observabilityv0.GraphNode_Type(0)
	}
	return observabilityv0.GraphNode_Type(observabilityv0.GraphNode_Type_value[strings.ToUpper(s)])
}

func ToGraphResponse(g *DAG) *observabilityv0.GraphResponse {
	resp := &observabilityv0.GraphResponse{}
	for _, node := range g.Nodes() {
		resp.Nodes = append(resp.Nodes, &observabilityv0.GraphNode{
			Id:   node.ID,
			Type: ToType(node.Type),
		})
	}
	for _, edge := range g.Edges() {
		resp.Edges = append(resp.Edges, &observabilityv0.GraphEdge{
			From: edge.From,
			To:   edge.To,
		})
	}
	return resp
}

// Cycle returns a cycle as the sequence of nodes traversed to come back to the
// first one, or nil when the graph is acyclic. Reporting the path is what lets
// a caller say WHICH services deadlock instead of only that some do. Node
// iteration is sorted so the reported cycle is stable across runs.
func (g *DAG) Cycle() []string {
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(g.nodes))
	var stack []string
	var cycle []string

	var visit func(node string) bool
	visit = func(node string) bool {
		state[node] = onStack
		stack = append(stack, node)
		children := slices.Clone(g.edges[node])
		sort.Strings(children)
		for _, child := range children {
			switch state[child] {
			case unvisited:
				if visit(child) {
					return true
				}
			case onStack:
				start := slices.Index(stack, child)
				cycle = append(slices.Clone(stack[start:]), child)
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = done
		return false
	}

	nodes := make([]string, 0, len(g.nodes))
	for node := range g.nodes {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if state[node] == unvisited && visit(node) {
			return cycle
		}
	}
	return nil
}

func (g *DAG) TopologicalSort() ([]Node, error) {
	var sorted []string
	var queue []string

	// Work on a COPY of the in-degree counts. The previous version decremented
	// g.incomingEdges in place, so a second TopologicalSort on the same DAG saw
	// already-zeroed (or negative) counts and returned a non-topological order.
	inDegree := make(map[string]int, len(g.incomingEdges))
	for node, deg := range g.incomingEdges {
		inDegree[node] = deg
	}

	// Find all nodes with no incoming edges
	for node := range g.nodes {
		if inDegree[node] == 0 {
			queue = append(queue, node)
		}
	}

	for len(queue) > 0 {
		// Sort queue to ensure deterministic output
		sort.Strings(queue)
		node := queue[0]
		queue = queue[1:]

		sorted = append(sorted, node)

		for _, neighbor := range g.edges[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if len(sorted) != len(g.nodes) {
		return nil, fmt.Errorf("graph has a cycle: %s", strings.Join(g.Cycle(), " -> "))
	}
	var out []Node
	for _, node := range sorted {
		n := g.Node(node)
		if n != nil {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (g *DAG) TopologicalSortTo(endNode string) ([]Node, error) {
	sub, err := g.SubGraphTo(endNode)
	if err != nil {
		return nil, fmt.Errorf("cannot invert graph: %w", err)
	}
	sub = sub.Invert()
	sorted, err := sub.TopologicalSortFrom(endNode)
	if err != nil {
		return nil, fmt.Errorf("cannot sort graph to endNode %s", endNode)
	}
	slices.Reverse(sorted)
	return sorted, nil
}

// ReachableFrom returns true if the endNode is reachable from the startNode
func (g *DAG) ReachableFrom(startNode string, endNode string) bool {
	sub, err := g.SubGraphFrom(startNode)
	if err != nil {
		return false
	}
	return sub.HasNode(endNode)
}

func (g *DAG) TopologicalSortFrom(startNode string) ([]Node, error) {
	sub, err := g.SubGraphFrom(startNode)
	if err != nil {
		return nil, fmt.Errorf("cannot invert graph: %w", err)
	}
	sorted, err := sub.TopologicalSort()
	if err != nil {
		return nil, fmt.Errorf("cannot sort graph to startNode %s", startNode)
	}
	if len(sorted) < 2 {
		return nil, nil
	}
	sorted = sorted[1:]
	return sorted, nil
}

// SubGraphFrom returns a subgraph that contains all nodes that are reachable from the given node.
func (g *DAG) SubGraphFrom(startNode string) (*DAG, error) {
	if !g.HasNode(startNode) {
		return nil, fmt.Errorf("cannot create subgraph to node %s: node does not exist", startNode)
	}
	subGraph := NewDAG(fmt.Sprintf("%s-reachable-from-%s", g.Name, startNode))
	subGraph.verb = g.verb
	visited := make(map[string]bool)

	var dfs func(node string)
	dfs = func(node string) {
		visited[node] = true
		subGraph.AddNode(node).WithType(g.nodeTypes[node])
		if g.nodeTypes[node] != nil {
			subGraph.nodeTypes[node] = g.nodeTypes[node]
		}

		for _, child := range g.edges[node] {
			if !visited[child] {
				subGraph.AddNode(child).WithType(g.nodeTypes[child])
				subGraph.AddEdge(node, child)
				subGraph.inheritEdgeKinds(g, Edge{From: node, To: child})
				dfs(child)
			} else if subGraph.HasNode(child) {
				subGraph.AddEdge(node, child)
				subGraph.inheritEdgeKinds(g, Edge{From: node, To: child})
			}
		}
	}

	dfs(startNode)

	return subGraph, nil
}

func (g *DAG) SubGraphTo(endNode string) (*DAG, error) {
	if !g.HasNode(endNode) {
		return nil, fmt.Errorf("cannot create subgraph to node %s: node does not exist", endNode)
	}
	subGraph := NewDAG(fmt.Sprintf("%s-reachable-to-%s", g.Name, endNode))
	subGraph.verb = g.verb
	visited := make(map[string]bool)

	var dfs func(node string)
	dfs = func(node string) {
		visited[node] = true
		subGraph.AddNode(node).WithType(g.nodeTypes[node])
		if g.nodeTypes[node] != nil {
			subGraph.nodeTypes[node] = g.nodeTypes[node]
		}

		for _, parent := range g.Parents(node) {
			if !visited[parent.ID] {
				subGraph.AddNode(parent.ID).WithType(g.nodeTypes[parent.ID])
				subGraph.AddEdge(parent.ID, node)
				subGraph.inheritEdgeKinds(g, Edge{From: parent.ID, To: node})
				dfs(parent.ID)
			} else if subGraph.HasNode(parent.ID) {
				subGraph.AddEdge(parent.ID, node)
				subGraph.inheritEdgeKinds(g, Edge{From: parent.ID, To: node})
			}
		}
	}
	dfs(endNode)
	return subGraph, nil
}

func (g *DAG) Parents(s string) []Node {
	var res []Node
	for node, edges := range g.edges {
		for _, edge := range edges {
			if edge == s {
				res = append(res, Node{
					ID:   node,
					Type: g.nodeTypes[node],
				})
			}
		}
	}
	return res
}

func (g *DAG) Children(startNode string) []Node {
	var res []Node
	for _, edge := range g.edges[startNode] {
		res = append(res, Node{
			ID:   edge,
			Type: g.nodeTypes[edge],
		})
	}
	return res
}

func (g *DAG) SortedChildren(startNode string) ([]Node, error) {
	sorted, err := g.TopologicalSortFrom(startNode)
	if err != nil {
		return nil, fmt.Errorf("cannot sort graph to startNode %s", startNode)
	}
	var out []Node
	for _, node := range sorted {
		// Should be part of the edge
		if !slices.Contains(g.edges[startNode], node.ID) {
			continue
		}
		n := g.Node(node.ID)
		if n != nil {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (g *DAG) HasNode(node string) bool {
	return g.nodes[node]
}

func (g *DAG) SortedParents(endNode string) ([]Node, error) {
	sorted, err := g.TopologicalSortTo(endNode)
	if err != nil {
		return nil, fmt.Errorf("cannot sort graph to endNode %s", endNode)
	}
	var out []Node
	for _, node := range sorted {
		// Should be part of the edge
		if !slices.Contains(g.edges[node.ID], endNode) {
			continue
		}
		n := g.Node(node.ID)
		if n != nil {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (g *DAG) HasEdge(from string, to string) bool {
	return slices.Contains(g.edges[from], to)
}
