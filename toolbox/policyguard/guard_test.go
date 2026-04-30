package policyguard_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/toolbox/git"
	"github.com/codefly-dev/core/toolbox/policyguard"
)

// gitWorkspace materializes a real one-commit git repo. We use the
// real git toolbox under the guard so the test exercises the full
// stack (guard → real toolbox → real go-git → real repo) rather
// than a fake.
func gitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add", "README.md"},
	} {
		_ = args // silence unused warning before file write
	}
	require.NoError(t, runGit(dir, "init"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# test\n"), 0o600))
	require.NoError(t, runGit(dir, "add", "README.md"))
	require.NoError(t, runGitWithEnv(dir, "commit", "-m", "initial"))
	return dir
}

func TestGuard_Identity_Passthrough(t *testing.T) {
	// Even with a deny-all PDP, Identity must still succeed. Identity
	// describes the toolbox; refusing it would defeat catalog UIs.
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), policy.DenyAllPDP{}, "git")
	resp, err := g.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "git", resp.Name,
		"Identity must always pass through; PDPs only gate side-effecting RPCs")
}

func TestGuard_ListTools_Passthrough(t *testing.T) {
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), policy.DenyAllPDP{}, "git")
	resp, err := g.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Tools,
		"ListTools must pass through even under deny-all — agents need to know what's offered before they can ask for permission")
}

func TestGuard_CallTool_AllowAll_PassesThrough(t *testing.T) {
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), policy.AllowAllPDP{}, "git")
	resp, err := g.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "allow-all PDP must not refuse any call")
	require.NotEmpty(t, resp.Content, "allow-all must pass through to the real implementation")
}

func TestGuard_CallTool_DenyAll_RefusesWithReason(t *testing.T) {
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), policy.DenyAllPDP{}, "git")
	resp, err := g.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err, "policy refusal is a tool-level error, not a transport error")
	require.NotEmpty(t, resp.Error, "deny-all PDP must produce a refusal envelope")
	require.Contains(t, resp.Error, "deny-all",
		"the PDP's reason must surface verbatim so the agent sees WHY it was refused")
}

func TestGuard_CallTool_JSONPolicy_Granular(t *testing.T) {
	// Real-world shape: status is allowed, log is not.
	pdp := policy.NewJSONPDP(policy.JSONPolicy{
		Default: "deny",
		Rules: []policy.PolicyRule{
			{Toolbox: "git", Tool: "git.status", Allow: true},
		},
	})
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), pdp, "git")

	// Allowed
	resp, _ := g.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "git.status"})
	require.Empty(t, resp.Error, "git.status must pass: %v", resp.Error)

	// Default-denied
	resp, _ = g.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "git.log"})
	require.NotEmpty(t, resp.Error,
		"git.log has no rule and default is deny — must be refused")
	require.Contains(t, resp.Error, "default-deny")
}

func TestGuard_NilPDP_DefaultsToAllowAll(t *testing.T) {
	// Construction with nil PDP must NOT panic and MUST behave like
	// allow-all. This is the migration safety net — installing the
	// guard with a not-yet-configured PDP should be a no-op.
	dir := gitWorkspace(t)
	g := policyguard.New(git.New(dir, "guard-test"), nil, "git")
	resp, err := g.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "nil PDP must default to allow-all (migration safety)")
}
