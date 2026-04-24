package code

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// NativeGit wraps a go-git Repository for in-process git operations.
// Eliminates fork/exec overhead for common operations (log, show, diff).
// Falls back to exec for operations go-git doesn't support well (blame with line ranges).
type NativeGit struct {
	repo *git.Repository
	dir  string
}

// OpenNativeGit opens a git repository at dir. Returns nil (not error) if
// dir is not a git repo — callers should fall back to exec-based git.
func OpenNativeGit(dir string) *NativeGit {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil
	}
	return &NativeGit{repo: repo, dir: dir}
}

// Log returns recent commits, optionally filtered by path and since date.
//
// Walks the first-parent chain manually via CommitObject() rather than
// using repo.Log()'s iterator. On shallow clones (GitHub Actions
// default fetch-depth=1), go-git's log iterator tries to resolve the
// deepest commit's parent eagerly for DFS bookkeeping and fails with
// "object not found" BEFORE yielding even HEAD. Walking parent-by-
// parent keeps full control: every commit we do have is yielded, and
// the shallow boundary cleanly terminates iteration.
func (g *NativeGit) Log(ctx context.Context, maxCount int, ref, path, since string) ([]*GitCommitInfo, error) {
	if maxCount <= 0 {
		maxCount = 50
	}

	// Resolve the starting commit: caller-supplied ref, or HEAD.
	var startHash plumbing.Hash
	if ref != "" {
		h, err := g.repo.ResolveRevision(plumbing.Revision(ref))
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", ref, err)
		}
		startHash = *h
	} else {
		h, err := g.repo.Head()
		if err != nil {
			return nil, fmt.Errorf("head: %w", err)
		}
		startHash = h.Hash()
	}

	// Optional filters computed once.
	var pathPrefix string
	if path != "" {
		pathPrefix = path
	}
	var sinceTime *time.Time
	if since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			sinceTime = &t
		} else if t, err := time.Parse("2006-01-02", since); err == nil {
			sinceTime = &t
		}
	}

	var commits []*GitCommitInfo
	current := startHash
	seen := map[plumbing.Hash]bool{}
	for len(commits) < maxCount {
		if seen[current] {
			break // cycle guard, shouldn't happen in a real repo
		}
		seen[current] = true

		c, err := g.repo.CommitObject(current)
		if err != nil {
			if strings.Contains(err.Error(), "object not found") {
				break // shallow-clone boundary
			}
			// Surface other errors but still return what we have.
			return commits, fmt.Errorf("commit %s: %w", current.String()[:7], err)
		}

		// since filter: stop once we're past the cutoff.
		if sinceTime != nil && c.Author.When.Before(*sinceTime) {
			break
		}

		// path filter: skip commits that didn't touch the path. Needs
		// parent tree comparison; best-effort via Stats.
		if pathPrefix != "" {
			stats, statErr := c.Stats()
			if statErr == nil {
				touched := false
				for _, s := range stats {
					if s.Name == pathPrefix || strings.HasPrefix(s.Name, pathPrefix+"/") {
						touched = true
						break
					}
				}
				if !touched {
					// advance to first parent and continue
					if c.NumParents() == 0 {
						break
					}
					current = c.ParentHashes[0]
					continue
				}
			}
		}

		hash := c.Hash.String()
		ci := &GitCommitInfo{
			Hash:      hash,
			ShortHash: hash[:7],
			Author:    c.Author.Name,
			Date:      c.Author.When.Format(time.RFC3339),
			Message:   strings.Split(c.Message, "\n")[0], // first line only
		}
		if stats, err := c.Stats(); err == nil {
			ci.FilesChanged = int32(len(stats))
		}
		commits = append(commits, ci)

		if c.NumParents() == 0 {
			break // root commit
		}
		current = c.ParentHashes[0] // first-parent walk
	}

	return commits, nil
}

// Show returns the content of a file at a given ref.
func (g *NativeGit) Show(ctx context.Context, ref, path string) (string, bool, error) {
	if ref == "" {
		ref = "HEAD"
	}

	hash, err := g.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return "", false, nil
	}

	commit, err := g.repo.CommitObject(*hash)
	if err != nil {
		return "", false, nil
	}

	file, err := commit.File(path)
	if err != nil {
		return "", false, nil // file doesn't exist at this ref
	}

	content, err := file.Contents()
	if err != nil {
		return "", false, fmt.Errorf("read %s at %s: %w", path, ref, err)
	}

	return content, true, nil
}

// DiffStat returns file-level diff statistics between two refs.
// For full unified diff output, fall back to exec (go-git's patch is verbose).
func (g *NativeGit) DiffStat(ctx context.Context, baseRef, headRef string) ([]*GitDiffFileInfo, error) {
	base, err := g.resolveCommit(baseRef)
	if err != nil {
		return nil, fmt.Errorf("resolve base %s: %w", baseRef, err)
	}
	head, err := g.resolveCommit(headRef)
	if err != nil {
		return nil, fmt.Errorf("resolve head %s: %w", headRef, err)
	}

	baseTree, _ := base.Tree()
	headTree, _ := head.Tree()

	changes, err := baseTree.Diff(headTree)
	if err != nil {
		return nil, fmt.Errorf("diff: %w", err)
	}

	var files []*GitDiffFileInfo
	for _, change := range changes {
		fi := &GitDiffFileInfo{Path: changePath(change)}

		patch, pErr := change.Patch()
		if pErr == nil {
			for _, filePatch := range patch.FilePatches() {
				for _, chunk := range filePatch.Chunks() {
					lines := strings.Count(chunk.Content(), "\n")
					switch chunk.Type() {
					case 1: // Add
						fi.Additions += int32(lines)
					case 2: // Delete
						fi.Deletions += int32(lines)
					}
				}
			}
		}

		if fi.Additions > 0 && fi.Deletions == 0 {
			fi.Status = "added"
		} else if fi.Additions == 0 && fi.Deletions > 0 {
			fi.Status = "deleted"
		} else {
			fi.Status = "modified"
		}
		files = append(files, fi)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// ListBranches returns all local branch names.
func (g *NativeGit) ListBranches() ([]string, error) {
	iter, err := g.repo.Branches()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var names []string
	iter.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	})
	return names, nil
}

// HEAD returns the current HEAD commit hash.
func (g *NativeGit) HEAD() (string, error) {
	ref, err := g.repo.Head()
	if err != nil {
		return "", err
	}
	return ref.Hash().String(), nil
}

// --- helpers ---

func (g *NativeGit) resolveCommit(ref string) (*object.Commit, error) {
	if ref == "" {
		ref = "HEAD"
	}
	hash, err := g.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, err
	}
	return g.repo.CommitObject(*hash)
}

func changePath(change *object.Change) string {
	if change.To.Name != "" {
		return change.To.Name
	}
	return change.From.Name
}

// --- types (decoupled from proto) ---

// GitCommitInfo holds parsed commit data (used by both native and exec paths).
type GitCommitInfo struct {
	Hash         string
	ShortHash    string
	Author       string
	Date         string
	Message      string
	FilesChanged int32
}

// GitDiffFileInfo holds per-file diff statistics.
type GitDiffFileInfo struct {
	Path      string
	Additions int32
	Deletions int32
	Status    string
}

// --- integration with DefaultCodeServer ---

// openGitRepo lazily opens the native git repo. Returns nil if not a git repo.
func (s *DefaultCodeServer) openGitRepo() *NativeGit {
	if s.nativeGit != nil {
		return s.nativeGit
	}
	s.nativeGit = OpenNativeGit(s.SourceDir)
	return s.nativeGit
}

// closeGit closes the native git repo if open.
func (s *DefaultCodeServer) closeGit() {
	s.nativeGit = nil
}

// gitLogNative uses go-git when available, falls back to exec.
func (s *DefaultCodeServer) gitLogNative(ctx context.Context, req *codev0.GitLogRequest) (*codev0.CodeResponse, error) {
	if ng := s.openGitRepo(); ng != nil {
		commits, err := ng.Log(ctx, int(req.MaxCount), req.Ref, req.Path, req.Since)
		if err == nil {
			var protoCommits []*codev0.GitCommit
			for _, c := range commits {
				protoCommits = append(protoCommits, &codev0.GitCommit{
					Hash: c.Hash, ShortHash: c.ShortHash,
					Author: c.Author, Date: c.Date, Message: c.Message,
					FilesChanged: c.FilesChanged,
				})
			}
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitLog{GitLog: &codev0.GitLogResponse{Commits: protoCommits}}}, nil
		}
		// Fall through to exec on error
	}
	return s.gitLog(ctx, req)
}

// gitShowNative uses go-git when available, falls back to exec.
func (s *DefaultCodeServer) gitShowNative(ctx context.Context, req *codev0.GitShowRequest) (*codev0.CodeResponse, error) {
	if ng := s.openGitRepo(); ng != nil {
		content, exists, err := ng.Show(ctx, req.Ref, req.Path)
		if err == nil {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{
				Content: content, Exists: exists,
			}}}, nil
		}
	}
	return s.gitShow(ctx, req)
}
