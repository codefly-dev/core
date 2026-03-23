package code

import (
	"context"
	"math"
	"sort"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// ScoredFile is a file path annotated with a composite relevance score.
type ScoredFile struct {
	Path  string
	Score float64

	SearchHits  int
	SymbolHits  int
	Callers     int
	RecentLines int
	Importers   int
}

// RelevanceScorer ranks files by structural relevance to a text query.
// All signals are computed from data already available in CodebaseContext --
// no embeddings or LLM calls required.
type RelevanceScorer struct {
	graph    *CodeGraph
	depGraph *DepGraph
	timeline []*FileTimeline
	server   CodeExecutor

	wSearch    float64
	wSymbol    float64
	wCallGraph float64
	wRecency   float64
	wCentral   float64
}

// ScorerOption configures a RelevanceScorer.
type ScorerOption func(*RelevanceScorer)

// WithWeights sets custom signal weights. Default: 0.35, 0.25, 0.15, 0.15, 0.10.
func WithWeights(search, symbol, callGraph, recency, centrality float64) ScorerOption {
	return func(r *RelevanceScorer) {
		r.wSearch = search
		r.wSymbol = symbol
		r.wCallGraph = callGraph
		r.wRecency = recency
		r.wCentral = centrality
	}
}

// NewRelevanceScorer creates a scorer from a CodebaseContext and its backing server.
func NewRelevanceScorer(cc *CodebaseContext, server CodeExecutor, opts ...ScorerOption) *RelevanceScorer {
	r := &RelevanceScorer{
		graph:    cc.Graph,
		depGraph: cc.DepGraph,
		timeline: cc.Timelines,
		server:   server,

		wSearch:    0.35,
		wSymbol:    0.25,
		wCallGraph: 0.15,
		wRecency:   0.15,
		wCentral:   0.10,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ScoreFiles ranks the provided files by relevance to the query.
// Returns results sorted by descending score.
func (r *RelevanceScorer) ScoreFiles(ctx context.Context, query string, files []string) []ScoredFile {
	terms := tokenize(query)
	searchHits := r.searchSignal(ctx, terms)
	symbolHits := r.symbolSignal(terms)
	callerCounts := r.callGraphSignal(symbolHits)
	recentLines := r.recencySignal()
	importerCounts := r.centralitySignal()

	maxSearch := maxIntMap(searchHits)
	maxSymbol := maxIntMap(symbolHits)
	maxCallers := maxIntMap(callerCounts)
	maxRecent := maxIntMap(recentLines)
	maxImporters := maxIntMap(importerCounts)

	scored := make([]ScoredFile, 0, len(files))
	for _, f := range files {
		sf := ScoredFile{
			Path:        f,
			SearchHits:  searchHits[f],
			SymbolHits:  symbolHits[f],
			Callers:     callerCounts[f],
			RecentLines: recentLines[f],
			Importers:   importerCounts[f],
		}
		sf.Score = r.wSearch*norm(sf.SearchHits, maxSearch) +
			r.wSymbol*norm(sf.SymbolHits, maxSymbol) +
			r.wCallGraph*norm(sf.Callers, maxCallers) +
			r.wRecency*norm(sf.RecentLines, maxRecent) +
			r.wCentral*norm(sf.Importers, maxImporters)
		scored = append(scored, sf)
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	return scored
}

// TopK returns the top K files from ScoreFiles.
func (r *RelevanceScorer) TopK(ctx context.Context, query string, files []string, k int) []ScoredFile {
	all := r.ScoreFiles(ctx, query, files)
	if k >= len(all) {
		return all
	}
	return all[:k]
}

// --- Signal 1: Search hits (regex grep via Code server) ---

func (r *RelevanceScorer) searchSignal(ctx context.Context, terms []string) map[string]int {
	hits := make(map[string]int)
	if r.server == nil {
		return hits
	}
	pattern := strings.Join(terms, "|")
	resp, err := r.server.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern:    pattern,
			MaxResults: 500,
		}},
	})
	if err != nil {
		return hits
	}
	for _, m := range resp.GetSearch().Matches {
		hits[m.File]++
	}
	return hits
}

// --- Signal 2: Symbol name match ---

func (r *RelevanceScorer) symbolSignal(terms []string) map[string]int {
	hits := make(map[string]int)
	if r.graph == nil {
		return hits
	}
	for _, n := range r.graph.Nodes {
		if n.Kind == NodeFile || n.Kind == NodePackage {
			continue
		}
		lower := strings.ToLower(n.Name)
		for _, t := range terms {
			if strings.Contains(lower, t) {
				hits[n.File]++
				break
			}
		}
	}
	return hits
}

// --- Signal 3: Call graph proximity ---
// Files within 1 hop of symbol-matched files score higher.

func (r *RelevanceScorer) callGraphSignal(symbolHits map[string]int) map[string]int {
	counts := make(map[string]int)
	if r.graph == nil {
		return counts
	}
	var seedIDs []string
	for file := range symbolHits {
		seedIDs = append(seedIDs, r.graph.GetNodesForFile(file)...)
	}
	neighbors := r.graph.GetCallersOfAny(seedIDs)
	for _, nID := range neighbors {
		if n, ok := r.graph.Nodes[nID]; ok {
			counts[n.File]++
		}
	}
	for _, id := range seedIDs {
		for _, calleeID := range r.graph.GetCallees(id) {
			if n, ok := r.graph.Nodes[calleeID]; ok {
				counts[n.File]++
			}
		}
	}
	return counts
}

// --- Signal 4: Recency ---
// Count of recent-age lines per file.

func (r *RelevanceScorer) recencySignal() map[string]int {
	counts := make(map[string]int)
	for _, ft := range r.timeline {
		for _, c := range ft.Chunks {
			if c.Age == AgeRecent {
				counts[ft.Path] += c.EndLine - c.StartLine + 1
			}
		}
	}
	return counts
}

// --- Signal 5: Centrality ---
// Number of internal packages that import the package containing each file.

func (r *RelevanceScorer) centralitySignal() map[string]int {
	counts := make(map[string]int)
	if r.depGraph == nil {
		return counts
	}

	pkgImporters := make(map[string]int)
	for _, pkg := range r.depGraph.Packages {
		for _, imp := range pkg.Imports {
			trimmed := strings.TrimPrefix(imp, r.depGraph.Module+"/")
			pkgImporters[trimmed]++
		}
	}

	for _, pkg := range r.depGraph.Packages {
		n := pkgImporters[pkg.Path]
		for _, f := range pkg.Files {
			path := f
			if pkg.Path != "." {
				path = pkg.Path + "/" + f
			}
			counts[path] = n
		}
	}
	return counts
}

// --- Helpers ---

func tokenize(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'`()[]{}#")
		if len(w) >= 2 && !isStopWord(w) {
			terms = append(terms, w)
		}
	}
	return terms
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "are": true, "was": true, "were": true,
	"has": true, "have": true, "had": true, "not": true, "but": true,
	"all": true, "can": true, "her": true, "one": true, "our": true,
	"out": true, "you": true, "its": true, "how": true, "add": true,
}

func isStopWord(w string) bool { return stopWords[w] }

func norm(val, max int) float64 {
	if max <= 0 {
		return 0
	}
	return math.Min(float64(val)/float64(max), 1.0)
}

func maxIntMap(m map[string]int) int {
	max := 0
	for _, v := range m {
		if v > max {
			max = v
		}
	}
	return max
}
