package ciguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/internal/testgit"
)

// Splitting the dependency combination into an unprivileged assembler and a
// credential-bearing publisher put a git bundle between them, and that bundle
// is the whole reason the split works: the commits move as DATA, so the job
// holding the token never checks the assembled tree out.
//
// Nothing else exercises it. Both scripts call `gh`, so neither runs end to end
// outside Actions, and the handoff would first be tried on a Monday — where a
// wrong prerequisite or ref name means the week's dependency work silently does
// not happen, or, worse, that the publisher ends up on the combined tree after
// all.
//
// So the two git commands are lifted out of the scripts and run against real
// repositories. Lifted, not restated: a copy here could agree with this test
// and disagree with what runs.

const (
	planScript    = ".github/scripts/combine-deps-plan.sh"
	publishScript = ".github/scripts/combine-deps-publish.sh"
)

// lineContaining returns the single line of a repository script containing
// want, with leading indentation removed.
func lineContaining(t *testing.T, script, want string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), script))
	require.NoError(t, err)

	var found []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, want) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			found = append(found, strings.TrimSpace(line))
		}
	}
	require.Len(t, found, 1,
		"%s must contain exactly one command with %q (found %d). This test runs "+
			"that line verbatim, so it cannot choose between two.",
		script, want, len(found))
	return found[0]
}

// TestTheCombinedBranchTravelsAsABundleWithoutBeingCheckedOut builds the shape
// the two jobs are in -- an assembler with a combined branch, and a publisher
// that has only the default branch and the bundle -- and runs the real
// commands across it.
func TestTheCombinedBranchTravelsAsABundleWithoutBeingCheckedOut(t *testing.T) {
	bundleCommand := lineContaining(t, planScript, "git bundle create")
	fetchCommand := lineContaining(t, publishScript, "git fetch")

	root := t.TempDir()
	ctx := context.Background()
	git := func(dir string, args ...string) string {
		out, err := testgit.Run(ctx, dir, nil, args...)
		require.NoError(t, err, "git %s in %s: %s", strings.Join(args, " "), dir, out)
		return strings.TrimSpace(string(out))
	}

	// The shared remote both jobs clone from.
	origin := filepath.Join(root, "origin.git")
	require.NoError(t, os.MkdirAll(origin, 0o755))
	git(origin, "init", "--bare", "--initial-branch=main")

	// The assembler: default branch, plus the replayed bumps on deps/combined.
	planner := filepath.Join(root, "planner")
	require.NoError(t, os.MkdirAll(planner, 0o755))
	git(planner, "init", "--initial-branch=main")
	git(planner, "remote", "add", "origin", origin)
	for _, message := range []string{"base one", "base two"} {
		require.NoError(t, os.WriteFile(filepath.Join(planner, "go.mod"), []byte(message), 0o600))
		git(planner, "add", "go.mod")
		git(planner, "commit", "-m", message)
	}
	git(planner, "push", "--quiet", "origin", "main")
	git(planner, "fetch", "--quiet", "origin", "main")

	baseCommit := git(planner, "rev-parse", "main")
	git(planner, "checkout", "--quiet", "-B", "deps/combined", "origin/main")
	for _, message := range []string{"bump one", "bump two"} {
		require.NoError(t, os.WriteFile(filepath.Join(planner, "go.mod"), []byte(message), 0o600))
		git(planner, "add", "go.mod")
		git(planner, "commit", "-m", message)
	}
	combinedTip := git(planner, "rev-parse", "deps/combined")

	// Run the plan script's own bundle command.
	out := filepath.Join(root, "out")
	require.NoError(t, os.MkdirAll(out, 0o755))
	run := func(dir, script string, env ...string) (string, error) {
		command := exec.Command("bash", "-euo", "pipefail", "-c", script)
		command.Dir = dir
		command.Env = append(os.Environ(), env...)
		combined, err := command.CombinedOutput()
		return string(combined), err
	}

	output, err := run(planner, bundleCommand,
		"OUT="+out, "BASE=main", "BRANCH=deps/combined")
	require.NoError(t, err, "the plan script's bundle command failed:\n%s\n%s",
		bundleCommand, output)
	require.FileExists(t, filepath.Join(out, "combined.bundle"))

	// The publisher: a separate clone that has only the default branch, which
	// is exactly what `ref: main` gives it.
	publisher := filepath.Join(root, "publisher")
	git(root, "clone", "--quiet", origin, publisher)
	require.Equal(t, baseCommit, git(publisher, "rev-parse", "HEAD"),
		"the publisher starts on the default branch")

	output, err = run(publisher, fetchCommand,
		"IN="+out, "BRANCH=deps/combined")
	require.NoError(t, err, "the publish script's fetch command failed:\n%s\n%s",
		fetchCommand, output)

	// The commits arrived...
	require.Equal(t, combinedTip, git(publisher, "rev-parse", "refs/heads/deps/combined"),
		"the bundle must carry the combined branch to the publisher intact, or "+
			"the week's dependency work cannot be pushed")
	require.Equal(t, "bump two",
		git(publisher, "show", "-s", "--format=%s", "refs/heads/deps/combined"))

	// ...and nothing was checked out. This is the property the split exists
	// for: the token is in this repository, and the replayed tree is not in
	// its working directory.
	require.Equal(t, baseCommit, git(publisher, "rev-parse", "HEAD"),
		"the publisher's HEAD moved. The job holding the credential must stay "+
			"on the default branch; a fetch moves refs and must not move HEAD")
	body, err := os.ReadFile(filepath.Join(publisher, "go.mod"))
	require.NoError(t, err)
	require.Equal(t, "base two", string(body),
		"the publisher's working tree contains the combined content, so the "+
			"credential-bearing job is sitting on unreviewed files after all")

	// And the push a real run would make lands the branch on the remote
	// without the publisher ever visiting it.
	git(publisher, "push", "--quiet", "origin",
		"refs/heads/deps/combined:refs/heads/deps/combined")
	require.Equal(t, combinedTip, git(origin, "rev-parse", "refs/heads/deps/combined"))
}

// The split itself is pinned by content rather than by substring: both scripts
// are recorded in recordedFileDigests, so moving a line from one half to the
// other changes a digest and refuses. A `strings.Contains` over a script was
// the scanner's last form, and it could be satisfied by a comment.

// The publisher's own refusals, executed. `BASE` arrives in the artifact the
// unprivileged half writes, and the manifest allowlist is computed against it,
// so an unchecked value makes the allowlist meaningless two lines before the
// push with the PAT. These are the refusals that check it, run against a real
// repository rather than read.
func TestThePublisherRefusesABaseItCannotResolve(t *testing.T) {
	ctx := context.Background()
	script := filepath.Join(repoRoot(t), publishScript)

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"-c", "user.email=t@example.test", "-c", "user.name=t", "commit",
			"--allow-empty", "-m", "base"},
		// The remote-tracking ref the refusal resolves against. A publisher
		// clone has one; a repository with no remote must not be waved through.
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
	} {
		out, err := testgit.Run(ctx, repo, nil, args...)
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}

	for _, tc := range []struct{ name, base, refuses string }{
		{name: "multiline branch", base: "main\n--bad", refuses: "a branch is named by a bare name here"},
		{
			name: "a path rather than a branch",
			base: "../../etc/passwd", refuses: "a branch is named by a bare name here",
		},
		{
			name: "a revision range, which would diff something else entirely",
			base: "main..deps/combined", refuses: "not a branch name",
		},
		{
			name: "a branch this repository does not have",
			base: "no-such-branch", refuses: "it is not a branch of this repository",
		},
		{
			// The fail-open this replaces: `origin/<missing>` made `git diff`
			// fail inside a process substitution, which `set -euo pipefail`
			// does not cover, so `touched` was empty and the manifest
			// allowlist admitted every path in the bundle.
			name: "an empty value, which the allowlist used to admit everything for",
			base: "", refuses: "a branch is named by a bare name here",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(in, "combined.tsv"),
				[]byte("1\tdeps/one\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(in, "base"),
				[]byte(tc.base), 0o600))

			command := exec.Command("bash", script, in)
			command.Dir = repo
			command.Env = append(os.Environ(), "GH_TOKEN=unused")
			output, err := command.CombinedOutput()
			require.Error(t, err,
				"the publisher accepted base %q and carried on to the push:\n%s",
				tc.base, output)
			require.Contains(t, string(output), tc.refuses,
				"the publisher refused base %q for some other reason:\n%s",
				tc.base, output)
		})
	}
}

// And the other half of that defect: the diff the allowlist is computed from
// must be FATAL when it fails, not empty. The line is lifted from the script, so
// a revert to the process-substitution form fails here rather than passing.
// Extract the entire parsed statement, not an unterminated `if` line.
func manifestDiffStatement(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), publishScript))
	require.NoError(t, err)
	file, err := executionShell(string(raw))
	require.NoError(t, err)
	for _, stmt := range file.Stmts {
		if strings.Contains(shellText(stmt), "git diff --name-only") {
			return shellText(stmt)
		}
	}
	t.Fatal("publisher has no diff statement")
	return ""
}

func TestTheManifestAllowlistsDiffIsFatalWhenItFails(t *testing.T) {
	block := manifestDiffStatement(t)
	// bash -n must pass before a failing command can count as a refusal.
	syntaxCheck := exec.Command("bash", "-n", "-c", block)
	output, err := syntaxCheck.CombinedOutput()
	require.NoError(t, err, string(output))
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"commit", "--allow-empty", "-m", "base"},
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
	} {
		out, err := testgit.Run(context.Background(), root, nil, args...)
		require.NoError(t, err, string(out))
	}
	run := func(script string) (string, error) {
		command := exec.Command("bash", "-euo", "pipefail", "-c", script+"\necho REACHED-THE-NEXT-LINE")
		command.Dir = root
		command.Env = append(os.Environ(), "BASE=main", "BRANCH=deps/combined")
		out, err := command.CombinedOutput()
		return string(out), err
	}
	out, err := run(block)
	require.Error(t, err)
	require.Contains(t, out, "cannot diff the combined branch")
	require.NotContains(t, out, "REACHED-THE-NEXT-LINE")
	// A fail-open body must reach the sentinel. This proves syntax and an
	// unrelated failure are not what made the negative case green.
	out, err = run(strings.ReplaceAll(block, "exit 1", ":"))
	require.NoError(t, err, out)
	require.Contains(t, out, "REACHED-THE-NEXT-LINE")
	_, err = testgit.Run(context.Background(), root, nil, "branch", "deps/combined")
	require.NoError(t, err)
	out, err = run(block)
	require.NoError(t, err, out)
	require.Contains(t, out, "REACHED-THE-NEXT-LINE")
}

// The full publisher fetches the combined branch before diffing it, so a
// missing branch stops at fetch. A real bundle with unrelated history reaches
// the actual diff failure instead (both refs exist, but no merge base does).
func TestThePublisherRefusesAnUndiffableBundle(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		out, err := testgit.Run(context.Background(), root, nil, args...)
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "--initial-branch=main")
	git("commit", "--allow-empty", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("checkout", "--orphan", "deps/combined")
	git("commit", "--allow-empty", "-m", "unrelated")
	in := t.TempDir()
	git("bundle", "create", filepath.Join(in, "combined.bundle"), "refs/heads/deps/combined")
	git("checkout", "main")
	git("branch", "-D", "deps/combined")
	require.NoError(t, os.WriteFile(filepath.Join(in, "base"), []byte("main"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(in, "combined.tsv"), []byte("1\tdeps/one\n"), 0600))
	command := exec.Command("bash", filepath.Join(repoRoot(t), publishScript), in)
	command.Dir = root
	command.Env = append(os.Environ(), "GH_TOKEN=unused")
	out, err := command.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "cannot diff the combined branch")
	require.NotContains(t, string(out), "syntax error")
}
