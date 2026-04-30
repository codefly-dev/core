package git_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/git"
)

// initTestRepo materializes a real git repo in a tempdir, makes one
// initial commit, and returns the path. Tests that mutate state
// should make further commits themselves.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)

	// Write one file, stage it, commit. Without an initial commit
	// HEAD doesn't exist and `Log` errors.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# test repo\n"), 0o600))
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("README.md")
	require.NoError(t, err)
	_, err = wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
	return dir
}

func TestGit_Identity(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")
	resp, err := srv.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "git", resp.Name)
	require.Equal(t, "0.0.1", resp.Version)
	require.Equal(t, []string{"git"}, resp.CanonicalFor,
		"git toolbox owns the `git` binary; this drives the canonical-routing layer")
	require.NotEmpty(t, resp.SandboxSummary)
}

func TestGit_ListTools_Stable(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")
	resp, err := srv.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	// Pin the surface so adding/removing tools is a deliberate change.
	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{"git.status", "git.log", "git.diff"}, names,
		"if you change the tool surface, pin it here")

	for _, tl := range resp.Tools {
		require.NotEmpty(t, tl.Description, "every tool needs a description (the agent reads it to decide)")
		require.NotNil(t, tl.InputSchema, "every tool needs an input schema (even {} is fine)")
	}
}

func TestGit_Status_CleanRepoAfterInit(t *testing.T) {
	srv := git.New(initTestRepo(t), "0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "fresh repo with all files committed should be clean: %s", resp.Error)
	require.Len(t, resp.Content, 1)

	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, true, out["clean"])
	files, _ := out["files"].(map[string]any)
	require.Empty(t, files, "no working-tree changes after initial commit")
}

func TestGit_Status_DirtyAfterEdit(t *testing.T) {
	dir := initTestRepo(t)
	srv := git.New(dir, "0.0.1")

	// Edit the committed file — should show as modified in worktree.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# changed\n"), 0o600))

	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, false, out["clean"])
	files, _ := out["files"].(map[string]any)
	require.Contains(t, files, "README.md")
}

func TestGit_Log_ReturnsCommit(t *testing.T) {
	srv := git.New(initTestRepo(t), "0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"limit": 10})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "git.log",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	commits, _ := out["commits"].([]any)
	require.Len(t, commits, 1, "fresh repo has exactly one commit")

	first, _ := commits[0].(map[string]any)
	require.Equal(t, "initial commit", first["message"])
	require.NotEmpty(t, first["hash"])
	require.Equal(t, "Test", first["author"])
}

func TestGit_Log_RespectsLimit(t *testing.T) {
	dir := initTestRepo(t)
	repo, _ := gogit.PlainOpen(dir)
	wt, _ := repo.Worktree()
	for i := 0; i < 5; i++ {
		// Append to README to make a meaningful change each commit;
		// go-git refuses to commit nothing.
		path := filepath.Join(dir, "README.md")
		buf, _ := os.ReadFile(path)
		require.NoError(t, os.WriteFile(path, append(buf, []byte("\nedit\n")...), 0o600))
		_, _ = wt.Add("README.md")
		_, err := wt.Commit("edit", &gogit.CommitOptions{
			Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now().Add(time.Duration(i) * time.Second)},
		})
		require.NoError(t, err)
	}

	srv := git.New(dir, "0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"limit": 3})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "git.log",
		Arguments: args,
	})
	require.NoError(t, err)
	out := resp.Content[0].GetStructured().AsMap()
	commits, _ := out["commits"].([]any)
	require.Len(t, commits, 3, "limit=3 means 3 even though there are 6 total")
}

func TestGit_UnknownTool_ReturnsActionableError(t *testing.T) {
	srv := git.New(initTestRepo(t), "0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "git.bogus"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "git.bogus", "error must name the bad tool")
	require.Contains(t, resp.Error, "ListTools", "error should hint at discovery RPC")
}

func TestGit_Diff_ReturnsNotImplemented_ButDispatches(t *testing.T) {
	// The diff dispatch goes through the switch; the tool itself is a
	// stub. Pinning that the dispatch works guards the switch when we
	// later implement diff for real (just delete this test or assert
	// real content).
	srv := git.New(initTestRepo(t), "0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "git.diff"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "not yet implemented")
}
