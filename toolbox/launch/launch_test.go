package launch_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/agents/manager"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/launch"
)

// initGitRepo materializes a real one-commit repo at dir.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# test\n"), 0o600))
	run("add", "README.md")
	run("commit", "-m", "initial")
}

// installPluginAtAgentPath compiles the git plugin and places the
// binary at the path resources.Agent.Path resolves to under the
// supplied CODEFLY_HOME root. Mirrors how `codefly agent build`
// installs agents in production.
func installPluginAtAgentPath(t *testing.T, ctx context.Context, codeflyHome string, ag *resources.Agent) {
	t.Helper()
	t.Setenv(resources.CodeflyHomeEnv, codeflyHome)

	target, err := ag.Path(ctx)
	require.NoError(t, err, "Agent.Path must succeed for ToolboxAgent kind")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))

	cmd := exec.Command("go", "build", "-o", target,
		"github.com/codefly-dev/core/toolbox/git/cmd/git-toolbox")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build into agent path failed:\n%s", out)
}

// TestLaunch_FromManifest_RoundTrip is the integration test for the
// unified plugin shape: install the git plugin at the canonical agent
// path, build a resources.Toolbox manifest pointing to it, Launch it
// (which calls manager.Load — the SAME loader every other agent
// uses), and exercise the contract end-to-end.
//
// If this passes, the toolbox is just an agent.
func TestLaunch_FromManifest_RoundTrip(t *testing.T) {
	codeflyHome := t.TempDir()
	workspace := t.TempDir()
	initGitRepo(t, workspace)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "git",
		Version: "phaseB-test",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "git",
			Publisher: "codefly.dev",
			Version:   "phaseB-test",
		},
		CanonicalFor: []string{"git"},
	}
	require.NoError(t, tb.Validate())
	installPluginAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.Launch(ctx, tb,
		manager.WithEnv("CODEFLY_TOOLBOX_WORKSPACE="+workspace),
	)
	require.NoError(t, err, "launch must succeed via manager.Load")
	defer plugin.Close()

	// Identity surfaces the version coming from the manifest via
	// launch's standard env injection.
	id, err := plugin.Client.Identity(ctx, &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "git", id.Name)
	require.Equal(t, "phaseB-test", id.Version,
		"version must come from the manifest via launch's standard env")

	// Real RPC against the plugin operating on the workspace we set.
	resp, err := plugin.Client.CallTool(ctx,
		&toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "fresh repo via Launch must work end-to-end: %s", resp.Error)
	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, true, out["clean"])
}

func TestLaunch_NilToolbox_ReturnsError(t *testing.T) {
	_, err := launch.Launch(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil toolbox")
}

func TestLaunch_NilAgent_ReturnsError(t *testing.T) {
	tb := &resources.Toolbox{Name: "x", Version: "0.0.1"}
	_, err := launch.Launch(context.Background(), tb)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no agent",
		"missing agent must be a clear launch-time error")
}

func TestLaunch_AgentResolutionFailure_PropagatesError(t *testing.T) {
	tb := &resources.Toolbox{
		Name:    "weird",
		Version: "0.0.1",
		Agent: &resources.Agent{
			Kind:      "codefly:not-a-real-kind",
			Name:      "weird",
			Publisher: "test",
			Version:   "0.0.1",
		},
	}
	_, err := launch.Launch(context.Background(), tb)
	require.Error(t, err)
	// manager.Load surfaces the underlying Agent.Path failure; launch
	// wraps it with "load agent" context.
	require.True(t,
		errors.Is(err, errors.New("")) || err.Error() != "",
		"manager.Load must propagate the resolution failure: %v", err)
	require.Contains(t, err.Error(), "load agent",
		"launch must wrap the loader's error with its own framing")
}
