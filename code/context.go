package code

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// CodeExecutor is the interface every code server (Default, Go, …) satisfies.
type CodeExecutor interface {
	Execute(context.Context, *codev0.CodeRequest) (*codev0.CodeResponse, error)
}

// CodebaseContext holds all analysis layers assembled from a single code server.
// It is the unified input for LLM prompts, relevance scoring, and edit planning.
type CodebaseContext struct {
	Module    string
	Language  string
	Packages  []*codev0.PackageInfo
	CodeMap   *CodeMap
	DepGraph  *DepGraph
	Graph     *CodeGraph
	Timelines []*FileTimeline
	Stats     TimelineStats
}

// BuildCodebaseContext runs the full analysis pipeline through a CodeExecutor
// (typically GoCodeServer) and returns a populated CodebaseContext.
func BuildCodebaseContext(ctx context.Context, server CodeExecutor) (*CodebaseContext, error) {
	cc := &CodebaseContext{}

	infoResp, err := server.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		return nil, fmt.Errorf("get project info: %w", err)
	}
	info := infoResp.GetGetProjectInfo()
	cc.Module = info.Module
	cc.Language = info.Language
	cc.Packages = info.Packages

	var pkgInputs []PackageInput
	for _, p := range info.Packages {
		pkgInputs = append(pkgInputs, PackageInput{
			Name: p.Name, Path: p.RelativePath,
			Imports: p.Imports, Files: p.Files, Doc: p.Doc,
		})
	}
	cc.DepGraph = BuildDepGraph(info.Module, pkgInputs)

	symResp, err := server.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
	})
	if err != nil {
		return nil, fmt.Errorf("list symbols: %w", err)
	}
	syms := symResp.GetListSymbols()
	if syms.Status != nil && syms.Status.State == codev0.ListSymbolsStatus_SUCCESS {
		var inputs []SymbolInput
		for _, s := range syms.Symbols {
			file, line := "", 0
			if s.Location != nil {
				file = s.Location.File
				line = int(s.Location.Line)
			}
			inputs = append(inputs, SymbolInput{
				Name: s.Name, Kind: s.Kind.String(), Signature: s.Signature,
				File: file, Line: line, Parent: s.Parent,
			})
		}
		cc.CodeMap = BuildCodeMap(cc.Language, inputs)
	}

	if gs, ok := server.(*GoCodeServer); ok {
		if asp, ok := gs.symbols.(*ASTSymbolProvider); ok {
			if g, err := asp.Graph(); err == nil {
				cc.Graph = g
			}
		}
	}

	timelines, err := BuildProjectTimeline(ctx, server, []string{".go"}, time.Now())
	if err == nil && len(timelines) > 0 {
		cc.Timelines = timelines
		cc.Stats = ComputeTimelineStats(timelines)
	}

	return cc, nil
}

// Format produces a token-budgeted text representation for LLM system prompts.
// Budget is in bytes; 0 means unlimited. Sections are included in priority order:
// header > code map > dep graph > timeline > call graph.
func (cc *CodebaseContext) Format(budget int) string {
	var b strings.Builder

	header := cc.formatHeader()
	b.WriteString(header)
	if budget > 0 && b.Len() >= budget {
		return truncate(b.String(), budget)
	}

	if cc.CodeMap != nil {
		section := cc.CodeMap.Format()
		if budget <= 0 || b.Len()+len(section) < budget {
			b.WriteString(section)
		}
	}

	if cc.DepGraph != nil && len(cc.DepGraph.Packages) > 1 {
		section := cc.DepGraph.Format()
		if budget <= 0 || b.Len()+len(section) < budget {
			b.WriteString(section)
		}
	}

	if cc.Stats.TotalFiles > 0 {
		section := FormatTimelineStats(cc.Stats)
		if budget <= 0 || b.Len()+len(section) < budget {
			b.WriteString(section)
		}
	}

	if cc.Graph != nil {
		section := cc.formatCallGraph()
		if budget <= 0 || b.Len()+len(section) < budget {
			b.WriteString(section)
		}
	}

	if budget > 0 {
		return truncate(b.String(), budget)
	}
	return b.String()
}

func (cc *CodebaseContext) formatHeader() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Codebase: %s (%s)\n\n", cc.Module, cc.Language))

	if len(cc.Packages) > 0 {
		b.WriteString(fmt.Sprintf("Packages: %d\n", len(cc.Packages)))
	}
	if cc.CodeMap != nil {
		s := cc.CodeMap.Stats()
		b.WriteString(fmt.Sprintf("Files: %d, Symbols: %d\n", s.Files, s.Symbols))
	}
	if cc.Stats.TotalFiles > 0 {
		recent := cc.Stats.LinesByAge[AgeRecent]
		old := cc.Stats.LinesByAge[AgeOld] + cc.Stats.LinesByAge[AgeAncient]
		b.WriteString(fmt.Sprintf("Lines: %d (recent: %d, old: %d)\n", cc.Stats.TotalLines, recent, old))
	}
	b.WriteString("\n")
	return b.String()
}

func (cc *CodebaseContext) formatCallGraph() string {
	var b strings.Builder
	b.WriteString("# Call Graph (top callers/callees)\n\n")

	type nodeScore struct {
		id    string
		name  string
		score int
	}

	var scored []nodeScore
	for id, n := range cc.Graph.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		callers := cc.Graph.GetCallers(id)
		callees := cc.Graph.GetCallees(id)
		scored = append(scored, nodeScore{id: id, name: n.Name, score: len(callers) + len(callees)})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	limit := 20
	if len(scored) < limit {
		limit = len(scored)
	}
	for _, ns := range scored[:limit] {
		callers := cc.Graph.GetCallers(ns.id)
		callees := cc.Graph.GetCallees(ns.id)
		b.WriteString(fmt.Sprintf("  %s  callers=%d callees=%d\n", ns.name, len(callers), len(callees)))
	}
	b.WriteString("\n")
	return b.String()
}

// FilePaths returns all source file paths known to this context.
func (cc *CodebaseContext) FilePaths() []string {
	seen := make(map[string]bool)
	var paths []string

	if cc.CodeMap != nil {
		for _, f := range cc.CodeMap.Files {
			if !seen[f.Path] {
				seen[f.Path] = true
				paths = append(paths, f.Path)
			}
		}
	}
	if cc.Graph != nil {
		for _, f := range cc.Graph.Files() {
			if !seen[f] {
				seen[f] = true
				paths = append(paths, f)
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
