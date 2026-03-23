package code

import (
	"context"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestGitLog_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewDefaultCodeServer(dir)
			ctx := context.Background()

			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_GitLog{GitLog: &codev0.GitLogRequest{MaxCount: 10}},
			})
			if err != nil {
				t.Fatal(err)
			}
			logResp := resp.GetGitLog()
			if logResp == nil {
				t.Fatal("nil GitLog response")
			}
			if logResp.Error != "" {
				t.Fatalf("git log error: %s", logResp.Error)
			}
			if len(logResp.Commits) == 0 {
				t.Fatal("expected commits, got 0")
			}
			for i, c := range logResp.Commits {
				if c.Hash == "" || c.Author == "" || c.Message == "" {
					t.Errorf("commit[%d] has empty fields: hash=%q author=%q msg=%q", i, c.Hash, c.Author, c.Message)
				}
				if len(c.Hash) != 40 {
					t.Errorf("commit[%d] hash length %d, want 40", i, len(c.Hash))
				}
			}
			t.Logf("%d commits, latest: %s %q", len(logResp.Commits), logResp.Commits[0].ShortHash, logResp.Commits[0].Message)
		})
	}
}

func TestGitLog_PathFilter(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)
	srv := NewDefaultCodeServer(dir)

	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitLog{GitLog: &codev0.GitLogRequest{
			MaxCount: 5,
			Path:     "color.go",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logResp := resp.GetGitLog()
	if logResp.Error != "" {
		t.Fatalf("git log error: %s", logResp.Error)
	}
	if len(logResp.Commits) == 0 {
		t.Fatal("expected commits touching color.go")
	}
	t.Logf("%d commits touching color.go", len(logResp.Commits))
}

func TestGitShow_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewDefaultCodeServer(dir)

			logResp := mustGitLog(t, srv, 1)
			hash := logResp.Commits[0].Hash

			resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_GitShow{GitShow: &codev0.GitShowRequest{
					Ref:  hash,
					Path: "go.mod",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			showResp := resp.GetGitShow()
			if showResp == nil {
				t.Fatal("nil GitShow response")
			}
			if !showResp.Exists {
				t.Skip("go.mod not found at HEAD (may be expected for some repos)")
			}
			if showResp.Content == "" {
				t.Error("go.mod exists but content is empty")
			}
			t.Logf("go.mod at %s: %d bytes", hash[:8], len(showResp.Content))
		})
	}
}

func TestGitShow_NonexistentPath(t *testing.T) {
	repo := AllTestRepos()[0]
	dir := EnsureRepo(t, repo)
	srv := NewDefaultCodeServer(dir)

	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitShow{GitShow: &codev0.GitShowRequest{
			Ref:  "HEAD",
			Path: "this/does/not/exist.go",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	showResp := resp.GetGitShow()
	if showResp.Exists {
		t.Error("expected Exists=false for nonexistent path")
	}
}

func TestGitDiff_BetweenCommits(t *testing.T) {
	repo := AllTestRepos()[4] // rs/zerolog has many commits
	dir := EnsureRepo(t, repo)
	srv := NewDefaultCodeServer(dir)

	logResp := mustGitLog(t, srv, 5)
	if len(logResp.Commits) < 2 {
		t.Skip("need at least 2 commits")
	}
	newer := logResp.Commits[0].Hash
	older := logResp.Commits[len(logResp.Commits)-1].Hash

	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitDiff{GitDiff: &codev0.GitDiffRequest{
			BaseRef:  older,
			HeadRef:  newer,
			StatOnly: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	diffResp := resp.GetGitDiff()
	if diffResp == nil {
		t.Fatal("nil GitDiff response")
	}
	if diffResp.Error != "" {
		t.Fatalf("git diff error: %s", diffResp.Error)
	}
	t.Logf("diff %s..%s: %d files changed", older[:8], newer[:8], len(diffResp.Files))
	for _, f := range diffResp.Files {
		t.Logf("  %s: +%d -%d (%s)", f.Path, f.Additions, f.Deletions, f.Status)
	}
}

func TestGitDiff_FullPatch(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)
	srv := NewDefaultCodeServer(dir)

	logResp := mustGitLog(t, srv, 3)
	if len(logResp.Commits) < 2 {
		t.Skip("need at least 2 commits")
	}

	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitDiff{GitDiff: &codev0.GitDiffRequest{
			BaseRef:      logResp.Commits[1].Hash,
			HeadRef:      logResp.Commits[0].Hash,
			ContextLines: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	diffResp := resp.GetGitDiff()
	if diffResp.Error != "" {
		t.Fatalf("git diff error: %s", diffResp.Error)
	}
	if diffResp.Diff == "" {
		t.Log("empty diff (commits may touch same tree)")
	} else {
		t.Logf("patch size: %d bytes", len(diffResp.Diff))
	}
}

func TestGitBlame_RealRepos(t *testing.T) {
	targets := []struct {
		repoIdx int
		path    string
	}{
		{0, "color.go"},
		{4, "log.go"},
		{5, "mux.go"},
	}

	repos := AllTestRepos()
	for _, tgt := range targets {
		repo := repos[tgt.repoIdx]
		t.Run(repo.Name+"/"+tgt.path, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewDefaultCodeServer(dir)

			resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_GitBlame{GitBlame: &codev0.GitBlameRequest{
					Path:      tgt.path,
					StartLine: 1,
					EndLine:   20,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			blameResp := resp.GetGitBlame()
			if blameResp == nil {
				t.Fatal("nil GitBlame response")
			}
			if blameResp.Error != "" {
				t.Fatalf("git blame error: %s", blameResp.Error)
			}
			if len(blameResp.Lines) == 0 {
				t.Fatal("expected blame lines")
			}
			for _, bl := range blameResp.Lines[:min(5, len(blameResp.Lines))] {
				t.Logf("  L%d %s %s: %s", bl.Line, bl.Hash[:8], bl.Author, bl.Content)
			}
			t.Logf("%d blame lines total", len(blameResp.Lines))
		})
	}
}

func mustGitLog(t *testing.T, srv *DefaultCodeServer, maxCount int) *codev0.GitLogResponse {
	t.Helper()
	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitLog{GitLog: &codev0.GitLogRequest{MaxCount: int32(maxCount)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logResp := resp.GetGitLog()
	if logResp == nil || logResp.Error != "" {
		t.Fatalf("git log failed: %v", logResp)
	}
	return logResp
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
