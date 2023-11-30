package overview

type Graph struct {
	Name  string
	Nodes map[string]bool
	Edges map[string][]string
}

func NewGraph(name string) *Graph {
	return &Graph{
		Name:  name,
		Nodes: make(map[string]bool),
		Edges: make(map[string][]string),
	}
}

func (g *Graph) AddNode(u string) {
	g.Nodes[u] = true
}

func (g *Graph) AddEdge(u, v string) {
	if !g.Nodes[u] {
		g.Nodes[u] = true
	}
	if !g.Nodes[v] {
		g.Nodes[v] = true
	}
	g.Edges[u] = append(g.Edges[u], v)
}

func (g *Graph) TopologicalSort() []string {
	visited := make(map[string]bool)
	var stack []string

	var dfs func(node string)

	dfs = func(node string) {
		visited[node] = true
		for _, n := range g.Edges[node] {
			if !visited[n] {
				dfs(n)
			}
		}
		stack = append([]string{node}, stack...)
	}

	for node := range g.Nodes {
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
