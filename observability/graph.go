package observability

type Graph struct {
	Name  string
	nodes map[string]bool
	edges map[string][]string
}

func NewGraph(name string) *Graph {
	return &Graph{
		Name:  name,
		nodes: make(map[string]bool),
		edges: make(map[string][]string),
	}
}

func (g *Graph) AddNode(u string) {
	g.nodes[u] = true
}

func (g *Graph) AddEdge(u, v string) {
	if !g.nodes[u] {
		g.nodes[u] = true
	}
	if !g.nodes[v] {
		g.nodes[v] = true
	}
	g.edges[u] = append(g.edges[u], v)
}

func (g *Graph) Nodes() []string {
	var nodes []string
	for node := range g.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

type Edge struct {
	From string
	To   string
}

func (g *Graph) Edges() []Edge {
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

func (g *Graph) TopologicalSort() []string {
	visited := make(map[string]bool)
	var stack []string

	var dfs func(node string)

	dfs = func(node string) {
		visited[node] = true
		for _, n := range g.edges[node] {
			if !visited[n] {
				dfs(n)
			}
		}
		stack = append([]string{node}, stack...)
	}

	for node := range g.nodes {
		if !visited[node] {
			dfs(node)
		}
	}
	return stack
}

func Reverse[T any](ss []T) {
	for i := len(ss)/2 - 1; i >= 0; i-- {
		opp := len(ss) - 1 - i
		ss[i], ss[opp] = ss[opp], ss[i]
	}
}
