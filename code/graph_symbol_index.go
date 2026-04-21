package code

import "strings"

// FindDefinitions returns all nodes whose name matches (case-insensitive).
// O(1) lookup via the nameIdx — no linear scan of all nodes.
func (g *CodeGraph) FindDefinitions(name string) []*CodeNode {
	key := strings.ToLower(name)
	ids := g.nameIdx[key]
	if len(ids) == 0 {
		return nil
	}
	return g.ResolveIDs(ids)
}

// FindDefinitionsByKind returns nodes matching both name and kind.
func (g *CodeGraph) FindDefinitionsByKind(name string, kind NodeKind) []*CodeNode {
	nodes := g.FindDefinitions(name)
	if len(nodes) == 0 {
		return nil
	}
	var filtered []*CodeNode
	for _, n := range nodes {
		if n.Kind == kind {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// FindUsages returns all callers of any node matching the given name.
// "Where is X used?" — finds definitions of X, then returns their callers.
func (g *CodeGraph) FindUsages(name string) []*CodeNode {
	defs := g.FindDefinitions(name)
	if len(defs) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		seen[d.ID] = true
	}

	var usages []*CodeNode
	for _, d := range defs {
		for _, callerID := range g.callers[d.ID] {
			if !seen[callerID] {
				seen[callerID] = true
				if n, ok := g.Nodes[callerID]; ok {
					usages = append(usages, n)
				}
			}
		}
	}
	return usages
}

// SearchSymbols returns all nodes whose name contains the query (case-insensitive).
// Uses the nameIdx for prefix filtering — much faster than scanning all nodes
// when the index has many entries.
func (g *CodeGraph) SearchSymbols(query string) []*CodeNode {
	query = strings.ToLower(query)
	var results []*CodeNode
	for key, ids := range g.nameIdx {
		if strings.Contains(key, query) {
			for _, id := range ids {
				if n, ok := g.Nodes[id]; ok {
					results = append(results, n)
				}
			}
		}
	}
	return results
}

// NameIndexSize returns the number of unique names in the symbol index.
func (g *CodeGraph) NameIndexSize() int {
	return len(g.nameIdx)
}
