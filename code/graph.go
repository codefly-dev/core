// Package code provides shared, language-agnostic primitives for code operations.
//
// Graph types (CodeNode, CodeGraph, Edge) are the universal representation of
// a codebase's structure. Language-specific parsers (parse_go.go, etc.) populate
// these types; Mind and plugins both consume them.
package code

import (
	"fmt"
	"strings"
)

// NodeKind classifies a code graph node.
type NodeKind int

const (
	NodeFunction NodeKind = iota // standalone function
	NodeMethod                   // method on a type
	NodeType                     // struct or interface declaration
	NodeFile                     // source file
	NodePackage                  // directory / package
)

func (k NodeKind) String() string {
	switch k {
	case NodeFunction:
		return "function"
	case NodeMethod:
		return "method"
	case NodeType:
		return "type"
	case NodeFile:
		return "file"
	case NodePackage:
		return "package"
	default:
		return "unknown"
	}
}

// CodeNode represents a single element in the code graph.
type CodeNode struct {
	ID        string   // unique: "pkg/api/router.go::SetupRoutes"
	Kind      NodeKind // function, method, type, file, package
	Name      string   // short name: "SetupRoutes"
	File      string   // relative path: "pkg/api/router.go"
	Line      int      // starting line number
	EndLine   int      // ending line number (0 if unknown)
	Signature string   // from AST/LSP: "func SetupRoutes(r chi.Router)"
	Doc       string   // documentation comment, if any
	Body      string   // source text of the node (for summary generation)

	// Relationship IDs.
	Children []string // functions/types contained in this file/package
	Calls    []string // outgoing: functions this node calls
	CalledBy []string // incoming: functions that call this node
	Imports  []string // package paths imported by this file
}

// QualifiedName returns "File::Name" for display.
func (n *CodeNode) QualifiedName() string {
	if n.File == "" {
		return n.Name
	}
	return fmt.Sprintf("%s::%s", n.File, n.Name)
}

// EdgeKind classifies a relationship between two nodes.
type EdgeKind int

const (
	EdgeCalls    EdgeKind = iota // function A calls function B
	EdgeContains                 // file/package contains function/type
	EdgeImports                  // file imports a package
)

func (e EdgeKind) String() string {
	switch e {
	case EdgeCalls:
		return "calls"
	case EdgeContains:
		return "contains"
	case EdgeImports:
		return "imports"
	default:
		return "unknown"
	}
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	From string   // source node ID
	To   string   // target node ID
	Kind EdgeKind // relationship type
}

// CodeGraph holds the full structural representation of a codebase.
type CodeGraph struct {
	Nodes map[string]*CodeNode // node ID → node
	Edges []Edge               // all directed edges

	// Precomputed indexes for fast lookup.
	callers map[string][]string // node ID → caller IDs
	callees map[string][]string // node ID → callee IDs
	fileIdx map[string][]string // file path → node IDs in that file
	nameIdx map[string][]string // lowercase name → node IDs (symbol index)
}

// NewCodeGraph creates an empty graph.
func NewCodeGraph() *CodeGraph {
	return &CodeGraph{
		Nodes:   make(map[string]*CodeNode),
		callers: make(map[string][]string),
		callees: make(map[string][]string),
		fileIdx: make(map[string][]string),
		nameIdx: make(map[string][]string),
	}
}

// AddNode inserts or replaces a node and updates the file and name indexes.
func (g *CodeGraph) AddNode(n *CodeNode) {
	g.Nodes[n.ID] = n
	if n.File != "" && n.Kind != NodeFile && n.Kind != NodePackage {
		g.fileIdx[n.File] = appendUniqueStr(g.fileIdx[n.File], n.ID)
	}
	if n.Name != "" {
		key := strings.ToLower(n.Name)
		g.nameIdx[key] = appendUniqueStr(g.nameIdx[key], n.ID)
	}
}

// AddEdge records a directed edge and updates caller/callee indexes.
func (g *CodeGraph) AddEdge(e Edge) {
	g.Edges = append(g.Edges, e)
	if e.Kind == EdgeCalls {
		g.callees[e.From] = appendUniqueStr(g.callees[e.From], e.To)
		g.callers[e.To] = appendUniqueStr(g.callers[e.To], e.From)
	}
}

// GetCallers returns IDs of nodes that call the given node.
func (g *CodeGraph) GetCallers(nodeID string) []string { return g.callers[nodeID] }

// GetCallees returns IDs of nodes that the given node calls.
func (g *CodeGraph) GetCallees(nodeID string) []string { return g.callees[nodeID] }

// GetSameFile returns IDs of all nodes in the same file as the given node.
func (g *CodeGraph) GetSameFile(nodeID string) []string {
	n, ok := g.Nodes[nodeID]
	if !ok {
		return nil
	}
	return g.fileIdx[n.File]
}

// GetNodesForFile returns all node IDs belonging to a specific file.
func (g *CodeGraph) GetNodesForFile(file string) []string { return g.fileIdx[file] }

// GetCallersOfAny returns the union of callers for a set of node IDs.
// Deduplicates and excludes IDs in the input set.
func (g *CodeGraph) GetCallersOfAny(nodeIDs []string) []string {
	exclude := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		exclude[id] = true
	}
	seen := make(map[string]bool)
	var result []string
	for _, id := range nodeIDs {
		for _, caller := range g.callers[id] {
			if !exclude[caller] && !seen[caller] {
				seen[caller] = true
				result = append(result, caller)
			}
		}
	}
	return result
}

// RemoveNode deletes a node and all its edges.
func (g *CodeGraph) RemoveNode(nodeID string) {
	node, ok := g.Nodes[nodeID]
	if !ok {
		return
	}
	if node.File != "" {
		g.fileIdx[node.File] = removeStr(g.fileIdx[node.File], nodeID)
	}
	if node.Name != "" {
		key := strings.ToLower(node.Name)
		g.nameIdx[key] = removeStr(g.nameIdx[key], nodeID)
		if len(g.nameIdx[key]) == 0 {
			delete(g.nameIdx, key)
		}
	}
	for _, callee := range g.callees[nodeID] {
		g.callers[callee] = removeStr(g.callers[callee], nodeID)
	}
	for _, caller := range g.callers[nodeID] {
		g.callees[caller] = removeStr(g.callees[caller], nodeID)
	}
	delete(g.callers, nodeID)
	delete(g.callees, nodeID)

	filtered := g.Edges[:0]
	for _, e := range g.Edges {
		if e.From != nodeID && e.To != nodeID {
			filtered = append(filtered, e)
		}
	}
	g.Edges = filtered
	delete(g.Nodes, nodeID)
}

// RemoveNodesForFile removes all nodes associated with a file.
// Returns the removed IDs.
func (g *CodeGraph) RemoveNodesForFile(file string) []string {
	ids := make([]string, len(g.fileIdx[file]))
	copy(ids, g.fileIdx[file])
	for _, id := range ids {
		g.RemoveNode(id)
	}
	if _, ok := g.Nodes[file]; ok {
		g.RemoveNode(file)
	}
	delete(g.fileIdx, file)
	return ids
}

// RebuildIndexes recomputes all indexes from raw Nodes and Edges.
// Call after deserializing from storage.
func (g *CodeGraph) RebuildIndexes() {
	g.callers = make(map[string][]string)
	g.callees = make(map[string][]string)
	g.fileIdx = make(map[string][]string)
	g.nameIdx = make(map[string][]string)

	for id, n := range g.Nodes {
		if n.File != "" && n.Kind != NodeFile && n.Kind != NodePackage {
			g.fileIdx[n.File] = appendUniqueStr(g.fileIdx[n.File], id)
		}
		if n.Name != "" {
			key := strings.ToLower(n.Name)
			g.nameIdx[key] = appendUniqueStr(g.nameIdx[key], id)
		}
	}
	for _, e := range g.Edges {
		if e.Kind == EdgeCalls {
			g.callees[e.From] = appendUniqueStr(g.callees[e.From], e.To)
			g.callers[e.To] = appendUniqueStr(g.callers[e.To], e.From)
		}
	}
}

// ResolveIDs maps node IDs to CodeNode pointers, skipping unknowns.
func (g *CodeGraph) ResolveIDs(ids []string) []*CodeNode {
	var nodes []*CodeNode
	for _, id := range ids {
		if n, ok := g.Nodes[id]; ok {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// Files returns all unique source files in the graph.
func (g *CodeGraph) Files() []string {
	files := make([]string, 0, len(g.fileIdx))
	for f := range g.fileIdx {
		files = append(files, f)
	}
	return files
}

// FunctionNodes returns all nodes of kind Function or Method.
func (g *CodeGraph) FunctionNodes() []*CodeNode {
	var fns []*CodeNode
	for _, n := range g.Nodes {
		if n.Kind == NodeFunction || n.Kind == NodeMethod {
			fns = append(fns, n)
		}
	}
	return fns
}

// SummaryableNodes returns all nodes worth generating summaries for
// (functions, methods, types, files — everything except packages).
func (g *CodeGraph) SummaryableNodes() []*CodeNode {
	var nodes []*CodeNode
	for _, n := range g.Nodes {
		if n.Kind != NodePackage {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// Stats returns aggregate counts.
func (g *CodeGraph) Stats() GraphStats {
	s := GraphStats{TotalNodes: len(g.Nodes), TotalEdges: len(g.Edges)}
	for _, n := range g.Nodes {
		switch n.Kind {
		case NodeFunction:
			s.Functions++
		case NodeMethod:
			s.Methods++
		case NodeType:
			s.Types++
		case NodeFile:
			s.Files++
		case NodePackage:
			s.Packages++
		}
	}
	return s
}

// GraphStats holds aggregate counts for a code graph.
type GraphStats struct {
	TotalNodes int
	TotalEdges int
	Functions  int
	Methods    int
	Types      int
	Files      int
	Packages   int
}

func (s GraphStats) String() string {
	return fmt.Sprintf("%d nodes (%d funcs, %d methods, %d types, %d files, %d pkgs), %d edges",
		s.TotalNodes, s.Functions, s.Methods, s.Types, s.Files, s.Packages, s.TotalEdges)
}

// ─── Neighborhood ────────────────────────────────────────────

// Neighborhood captures the structural context around a node.
type Neighborhood struct {
	Node     *CodeNode
	Callers  []*CodeNode
	Callees  []*CodeNode
	SameFile []*CodeNode
}

// GetNeighborhood returns the structural neighborhood of a node.
func (g *CodeGraph) GetNeighborhood(nodeID string) *Neighborhood {
	node, ok := g.Nodes[nodeID]
	if !ok {
		return nil
	}
	return &Neighborhood{
		Node:     node,
		Callers:  g.ResolveIDs(g.GetCallers(nodeID)),
		Callees:  g.ResolveIDs(g.GetCallees(nodeID)),
		SameFile: g.ResolveIDs(g.GetSameFile(nodeID)),
	}
}

// ─── Helpers ─────────────────────────────────────────────────

func appendUniqueStr(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func removeStr(slice []string, val string) []string {
	result := slice[:0]
	for _, s := range slice {
		if s != val {
			result = append(result, s)
		}
	}
	return result
}
