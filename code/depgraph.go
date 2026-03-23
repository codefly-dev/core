package code

import (
	"fmt"
	"sort"
	"strings"
)

// DepGraph represents the package dependency graph of a project.
// Nodes are packages; edges are import relationships.
type DepGraph struct {
	Module   string
	Packages []PackageNode
}

// PackageNode represents one package in the dependency graph.
type PackageNode struct {
	Name    string
	Path    string
	Imports []string
	Files   []string
	Doc     string
}

// BuildDepGraph constructs a dependency graph from package info.
type PackageInput struct {
	Name    string
	Path    string
	Imports []string
	Files   []string
	Doc     string
}

// BuildDepGraph creates a DepGraph from raw package data.
func BuildDepGraph(module string, packages []PackageInput) *DepGraph {
	var nodes []PackageNode
	for _, p := range packages {
		nodes = append(nodes, PackageNode{
			Name:    p.Name,
			Path:    p.Path,
			Imports: p.Imports,
			Files:   p.Files,
			Doc:     p.Doc,
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Path < nodes[j].Path
	})

	return &DepGraph{Module: module, Packages: nodes}
}

// InternalEdges returns only edges between packages within the same module.
func (g *DepGraph) InternalEdges() []DepEdge {
	var edges []DepEdge
	for _, pkg := range g.Packages {
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, g.Module) {
				edges = append(edges, DepEdge{From: pkg.Path, To: strings.TrimPrefix(imp, g.Module+"/")})
			}
		}
	}
	return edges
}

// DepEdge represents a directed dependency from one package to another.
type DepEdge struct {
	From string
	To   string
}

// Roots returns packages that are not imported by any other internal package.
func (g *DepGraph) Roots() []string {
	imported := map[string]bool{}
	for _, e := range g.InternalEdges() {
		imported[e.To] = true
	}

	var roots []string
	for _, pkg := range g.Packages {
		if !imported[pkg.Path] {
			roots = append(roots, pkg.Path)
		}
	}
	return roots
}

// Leaves returns packages that don't import any other internal package.
func (g *DepGraph) Leaves() []string {
	hasInternal := map[string]bool{}
	for _, e := range g.InternalEdges() {
		hasInternal[e.From] = true
	}

	var leaves []string
	for _, pkg := range g.Packages {
		if !hasInternal[pkg.Path] {
			leaves = append(leaves, pkg.Path)
		}
	}
	return leaves
}

// ConnectedComponents returns groups of packages that are connected via
// internal imports (undirected). Packages in different components have no
// import relationship and can be worked on independently.
func (g *DepGraph) ConnectedComponents() [][]string {
	pathSet := map[string]bool{}
	for _, pkg := range g.Packages {
		pathSet[pkg.Path] = true
	}

	adj := map[string]map[string]bool{}
	for _, pkg := range g.Packages {
		if adj[pkg.Path] == nil {
			adj[pkg.Path] = map[string]bool{}
		}
		for _, imp := range pkg.Imports {
			target := strings.TrimPrefix(imp, g.Module+"/")
			if pathSet[target] {
				adj[pkg.Path][target] = true
				if adj[target] == nil {
					adj[target] = map[string]bool{}
				}
				adj[target][pkg.Path] = true
			}
		}
	}

	visited := map[string]bool{}
	var components [][]string

	for _, pkg := range g.Packages {
		if visited[pkg.Path] {
			continue
		}
		var comp []string
		queue := []string{pkg.Path}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			if visited[node] {
				continue
			}
			visited[node] = true
			comp = append(comp, node)
			for neighbor := range adj[node] {
				if !visited[neighbor] {
					queue = append(queue, neighbor)
				}
			}
		}
		sort.Strings(comp)
		components = append(components, comp)
	}

	return components
}

// Format produces a compact text representation of the dependency graph.
func (g *DepGraph) Format() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Dependency Graph: %s\n\n", g.Module))

	for _, pkg := range g.Packages {
		b.WriteString(fmt.Sprintf("## %s", pkg.Path))
		if pkg.Doc != "" {
			b.WriteString(fmt.Sprintf("  — %s", pkg.Doc))
		}
		b.WriteString("\n")

		if len(pkg.Files) > 0 {
			b.WriteString(fmt.Sprintf("  files: %s\n", strings.Join(pkg.Files, ", ")))
		}

		internal := filterInternal(pkg.Imports, g.Module)
		if len(internal) > 0 {
			b.WriteString(fmt.Sprintf("  imports: %s\n", strings.Join(internal, ", ")))
		}

		external := filterExternal(pkg.Imports, g.Module)
		if len(external) > 0 {
			b.WriteString(fmt.Sprintf("  external: %s\n", strings.Join(external, ", ")))
		}
	}
	return b.String()
}

func filterInternal(imports []string, module string) []string {
	var out []string
	for _, imp := range imports {
		if strings.HasPrefix(imp, module) {
			out = append(out, strings.TrimPrefix(imp, module+"/"))
		}
	}
	sort.Strings(out)
	return out
}

func filterExternal(imports []string, module string) []string {
	var out []string
	for _, imp := range imports {
		if !strings.HasPrefix(imp, module) && !isStdLib(imp) {
			out = append(out, imp)
		}
	}
	sort.Strings(out)
	return out
}

func isStdLib(imp string) bool {
	return !strings.Contains(imp, ".")
}
