package code

import (
	"context"
	"fmt"
	"io"
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
func (g *NativeGit) Log(ctx context.Context, maxCount int, ref, path, since string) ([]*GitCommitInfo, error) {
	if maxCount <= 0 {
		maxCount = 50
	}

	opts := &git.LogOptions{Order: git.LogOrderCommitterTime}

	if ref != "" {
		hash, err := g.repo.ResolveRevision(plumbing.Revision(ref))
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", ref, err)
		}
		opts.From = *hash
	}

	if path != "" {
		opts.PathFilter = func(p string) bool { return p == path || strings.HasPrefix(p, path+"/") }
	}

	if since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = &t
		} else if t, err := time.Parse("2006-01-02", since); err == nil {
			opts.Since = &t
		}
	}

	iter, err := g.repo.Log(opts)
	if err != nil {
		return nil, fmt.Errorf("log: %w", err)
	}
	defer iter.Close()

	// Manual iteration instead of iter.ForEach so we can stop cleanly on
	// "object not found" (shallow clone: GitHub Actions defaults fetch-
	// depth=1 and the parent of the deepest commit isn't locally present).
	// ForEach propagates that error AFTER the last successful callback
	// which is fine for logic but worse for readability — the explicit
	// loop makes end-of-history handling obvious.
	var commits []*GitCommitInfo
	for {
		if len(commits) >= maxCount {
			break
		}
		c, iterErr := iter.Next()
		if iterErr == io.EOF {
			break
		}
		if iterErr != nil {
			if strings.Contains(iterErr.Error(), "object not found") {
				// Shallow clone boundary. Return whatever we collected.
				break
			}
			return commits, fmt.Errorf("log iter: %w", iterErr)
		}
		hash := c.Hash.String()
		ci := &GitCommitInfo{
			Hash:      hash,
			ShortHash: hash[:7],
			Author:    c.Author.Name,
			Date:      c.Author.When.Format(time.RFC3339),
			Message:   strings.Split(c.Message, "\n")[0], // first line only
		}
		// Stats() also needs the parent commit; same shallow-clone story.
		// Best-effort — a failing Stats just means 0 files_changed.
		if stats, err := c.Stats(); err == nil {
			ci.FilesChanged = int32(len(stats))
		}
		commits = append(commits, ci)
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
