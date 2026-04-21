package code

import "context"

// VCSProvider abstracts version control operations. Both git and jj implement
// this interface, allowing the DefaultCodeServer to dispatch VCS operations
// without knowing which VCS is in use.
//
// The response types (GitCommitInfo, GitDiffFileInfo, etc.) are VCS-agnostic
// despite their "Git" prefix — they map to the Code proto responses.
type VCSProvider interface {
	// Log returns recent commits, optionally filtered by ref, path, and date.
	Log(ctx context.Context, maxCount int, ref, path, since string) ([]*GitCommitInfo, error)

	// Diff returns a diff between two refs (or working copy if refs are empty).
	// Returns the full diff text and per-file statistics.
	Diff(ctx context.Context, baseRef, headRef, path string, contextLines int, statOnly bool) (string, []*GitDiffFileInfo, error)

	// Show returns the content of a file at a specific ref.
	Show(ctx context.Context, ref, path string) (string, bool, error)

	// Blame returns line-by-line authorship of a file.
	Blame(ctx context.Context, path string, startLine, endLine int32) ([]*GitBlameLine, error)
}

// GitCommitInfo, GitDiffFileInfo are defined in git_native.go.
// GitBlameLine is defined below — shared by git and jj providers.

// GitBlameLine holds blame information for a single line.
type GitBlameLine struct {
	Hash    string
	Author  string
	Date    string
	Line    int32
	Content string
}
