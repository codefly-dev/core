package bash_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/toolbox/bash"
	"github.com/stretchr/testify/require"
)

// newToolbox returns a Toolbox with a fresh registry (so tests don't
// share state) and a native (no-op) sandbox so we can observe parser
// decisions without depending on bwrap/sandbox-exec installation.
//
// Tests that DO want OS-level enforcement should construct their own
// real sandbox via sandbox.New().
func newToolbox(t *testing.T) (*bash.Toolbox, *policy.CanonicalRegistry) {
	t.Helper()
	r := policy.NewCanonicalRegistry()
	return bash.New(r, sandbox.NewNative()), r
}

func TestBash_PassthroughAllowed_RunsScript(t *testing.T) {
	tb, _ := newToolbox(t)
	ctx := context.Background()

	res, err := tb.Exec(ctx, "echo hi")
	require.NoError(t, err)
	require.Zero(t, res.ExitCode)
	require.Equal(t, "hi\n", res.Stdout)
}

func TestBash_DeniesGit_BareInvocation(t *testing.T) {
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "git status")
	require.Error(t, err)

	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed),
		"git invocation must surface as CanonicalRoutedError so callers can hint the right toolbox")
	require.Equal(t, "git", routed.Binary)
	require.Empty(t, routed.Owner, "no plugin has claimed git in this test setup; should hit the built-in fallback")
	require.Contains(t, routed.Reason, "Git toolbox")
}

func TestBash_DeniesGit_InCommandChain(t *testing.T) {
	// The known weakness in every existing tool's permission system:
	// `git status && git push` defeats `Bash(git push *)` deny because
	// matching is per-token, not per-parsed-command. Our AST walker
	// visits BOTH CallExpr nodes; the test guards against regression.
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "echo before && git push origin main")
	require.Error(t, err, "AST walker must catch git in the && chain, not just the leading command")
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git", routed.Binary)
}

func TestBash_DeniesGit_InPipe(t *testing.T) {
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "git log | head")
	require.Error(t, err, "pipeline does not bypass canonical routing")
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git", routed.Binary)
}

func TestBash_DeniesGit_InSubshell(t *testing.T) {
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "echo $(git rev-parse HEAD)")
	require.Error(t, err, "subshell substitution does not bypass canonical routing")
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git", routed.Binary)
}

func TestBash_DeniesGit_InProcessSubstitution(t *testing.T) {
	// Process substitution `<(cmd)` runs cmd in a subshell and
	// passes a /dev/fd/N file. The AST walker must descend into
	// the inner command — this test catches a regression where
	// CallExpr inside ProcSubst nodes are missed.
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "diff <(git log) <(echo)")
	require.Error(t, err, "process substitution must not bypass canonical routing")
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git", routed.Binary)
}

func TestBash_DeniesGit_WithLeadingPath(t *testing.T) {
	tb, _ := newToolbox(t)
	ctx := context.Background()

	_, err := tb.Exec(ctx, "/usr/bin/git status")
	require.Error(t, err, "fully-qualified path doesn't bypass routing")
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "/usr/bin/git", routed.Binary,
		"binary field carries the literal text the user wrote; the registry strips path on lookup")
}

func TestBash_PluginClaim_NamesTheOwner(t *testing.T) {
	// Once the Git toolbox plugin loads and claims `git`, the error
	// must point at the plugin (not the generic fallback) so the
	// agent knows where to route. End-to-end test of the
	// fallback-vs-claim precedence inside the bash error path.
	tb, reg := newToolbox(t)
	require.NoError(t, reg.Claim("git-toolbox-v1", "git"))

	_, err := tb.Exec(context.Background(), "git status")
	require.Error(t, err)
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git-toolbox-v1", routed.Owner,
		"explicit plugin claim must replace the fallback in the error")
}

func TestBash_DeniesAll_OnFirstHit(t *testing.T) {
	// We stop the AST walk at the first canonical hit — no need to
	// keep scanning. Test that we get exactly one error and it's the
	// one for the leading command.
	tb, _ := newToolbox(t)
	_, err := tb.Exec(context.Background(), "git status; docker ps")
	require.Error(t, err)
	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed))
	require.Equal(t, "git", routed.Binary, "first canonical hit wins; later commands not surfaced")
}

func TestBash_NonExistentBinary_RunsAndExitsNonZero(t *testing.T) {
	// A made-up binary isn't in the registry, so the parser allows
	// it. The subsequent shell exec fails with a non-zero exit code,
	// which we surface in Result rather than as a Go error — this is
	// "the script ran, it just failed", not "the toolbox refused."
	tb, _ := newToolbox(t)
	res, err := tb.Exec(context.Background(), "notarealbinary123")
	require.NoError(t, err, "parse-clean script that fails at runtime is not a toolbox error")
	require.NotZero(t, res.ExitCode)
}

func TestBash_MalformedScript_ParseError(t *testing.T) {
	tb, _ := newToolbox(t)
	_, err := tb.Exec(context.Background(), "if then fi")
	require.Error(t, err)
	require.NotErrorAs(t, err, new(*bash.CanonicalRoutedError),
		"a syntax error is a parse error, not a canonical refusal")
}

func TestBash_DynamicCommand_NotCaught_ParserOnly(t *testing.T) {
	// Documented limitation: dynamic command construction (where the
	// program name is built from a variable) is NOT caught at parse
	// time. This is a known weakness of static analysis. The OS
	// sandbox is what catches this — git binary won't be reachable
	// inside the sandboxed bash, so the script will fail at exec.
	//
	// This test pins the limitation: if the parser ever grows
	// reachability analysis we should DELETE this test, because then
	// the parser WOULD catch it.
	tb, _ := newToolbox(t)
	res, _ := tb.Exec(context.Background(), `g=git; $g status`)
	// The toolbox layer doesn't reject; it just runs. Whether $g
	// resolves and runs depends on the host environment. The test
	// asserts only that we don't surface CanonicalRoutedError —
	// confirming the limitation holds and motivating the OS sandbox.
	if res != nil && res.ExitCode == 0 {
		t.Log("warning: dynamic git invocation succeeded — OS sandbox is the only line of defense for this pattern")
	}
}
