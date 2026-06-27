package code

import (
	"context"
	"fmt"
	"strings"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// CodeExecutor is the interface every code server (Default, Go, …) satisfies.
type CodeExecutor interface {
	Execute(context.Context, *codev0.CodeRequest) (*codev0.CodeResponse, error)
}

// VFSProvider is implemented by servers that expose their underlying VFS and
// root directory for in-process use (e.g. relevance scoring, timeline building).
type VFSProvider interface {
	GetVFS() VFS
	GetSourceDir() string
}

// CodebaseContext holds all analysis layers assembled from a single code server.
// It is the unified input for LLM prompts, relevance scoring, and edit planning.
type CodebaseContext struct {
	Module    string
	Language  string
	Packages  []*codev0.PackageInfo
	DepGraph  *DepGraph
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

	if vp, ok := server.(VFSProvider); ok {
		timelines, err := BuildProjectTimeline(ctx, vp.GetVFS(), vp.GetSourceDir(), []string{".go"}, time.Now())
		if err == nil && len(timelines) > 0 {
			cc.Timelines = timelines
			cc.Stats = ComputeTimelineStats(timelines)
		}
	}

	return cc, nil
}

// Format produces a token-budgeted text representation for LLM system prompts.
// Budget is in bytes; 0 means unlimited. Sections are included in priority order:
// header > dep graph > timeline.
func (cc *CodebaseContext) Format(budget int) string {
	var b strings.Builder

	header := cc.formatHeader()
	b.WriteString(header)
	if budget > 0 && b.Len() >= budget {
		return truncate(b.String(), budget)
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
	if cc.Stats.TotalFiles > 0 {
		recent := cc.Stats.LinesByAge[AgeRecent]
		old := cc.Stats.LinesByAge[AgeOld] + cc.Stats.LinesByAge[AgeAncient]
		b.WriteString(fmt.Sprintf("Lines: %d (recent: %d, old: %d)\n", cc.Stats.TotalLines, recent, old))
	}
	b.WriteString("\n")
	return b.String()
}

// FilePaths returns all source file paths known to this context.
func (cc *CodebaseContext) FilePaths() []string {
	seen := make(map[string]bool)
	var paths []string

	for _, pkg := range cc.Packages {
		if pkg == nil {
			continue
		}
		prefix := pkg.RelativePath
		for _, file := range pkg.Files {
			path := file
			if prefix != "" && prefix != "." {
				path = prefix + "/" + file
			}
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}

	for _, timeline := range cc.Timelines {
		if timeline == nil || timeline.Path == "" || seen[timeline.Path] {
			continue
		}
		seen[timeline.Path] = true
		paths = append(paths, timeline.Path)
	}

	return paths
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
