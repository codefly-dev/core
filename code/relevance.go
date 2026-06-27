package code

import (
	"context"
	"math"
	"sort"
	"strings"
)

// ScoredFile is a file path annotated with a composite relevance score.
type ScoredFile struct {
	Path  string
	Score float64

	SearchHits  int
	RecentLines int
	Importers   int
}

// RelevanceScorer ranks files by operational relevance to a text query.
// All signals are computed from data already available in CodebaseContext --
// no embeddings or LLM calls required.
type RelevanceScorer struct {
	depGraph *DepGraph
	timeline []*FileTimeline
	vfs      VFS
	rootDir  string

	wSearch  float64
	wRecency float64
	wCentral float64
}

// ScorerOption configures a RelevanceScorer.
type ScorerOption func(*RelevanceScorer)

// WithWeights sets custom signal weights. Default: 0.60, 0.25, 0.15.
func WithWeights(search, recency, centrality float64) ScorerOption {
	return func(r *RelevanceScorer) {
		r.wSearch = search
		r.wRecency = recency
		r.wCentral = centrality
	}
}

// NewRelevanceScorer creates a scorer from a CodebaseContext and its backing server.
// vfs and rootDir are used for the search signal; pass a DefaultCodeServer's FS and SourceDir.
func NewRelevanceScorer(cc *CodebaseContext, vfs VFS, rootDir string, opts ...ScorerOption) *RelevanceScorer {
	r := &RelevanceScorer{
		depGraph: cc.DepGraph,
		timeline: cc.Timelines,
		vfs:      vfs,
		rootDir:  rootDir,

		wSearch:  0.60,
		wRecency: 0.25,
		wCentral: 0.15,
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
	recentLines := r.recencySignal()
	importerCounts := r.centralitySignal()

	maxSearch := maxIntMap(searchHits)
	maxRecent := maxIntMap(recentLines)
	maxImporters := maxIntMap(importerCounts)

	scored := make([]ScoredFile, 0, len(files))
	for _, f := range files {
		sf := ScoredFile{
			Path:        f,
			SearchHits:  searchHits[f],
			RecentLines: recentLines[f],
			Importers:   importerCounts[f],
		}
		sf.Score = r.wSearch*norm(sf.SearchHits, maxSearch) +
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

// --- Signal 1: Search hits (regex grep via VFS) ---

func (r *RelevanceScorer) searchSignal(ctx context.Context, terms []string) map[string]int {
	hits := make(map[string]int)
	if r.vfs == nil || r.rootDir == "" {
		return hits
	}
	pattern := strings.Join(terms, "|")
	var result *SearchResult
	var err error
	if _, ok := r.vfs.(LocalVFS); ok {
		result, err = Search(ctx, r.rootDir, SearchOpts{Pattern: pattern, MaxResults: 500})
	} else {
		result, err = SearchVFS(ctx, r.vfs, r.rootDir, SearchOpts{Pattern: pattern, MaxResults: 500})
	}
	if err != nil {
		return hits
	}
	for _, m := range result.Matches {
		hits[m.File]++
	}
	return hits
}

// --- Signal 2: Recency ---
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

// --- Signal 3: Centrality ---
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
