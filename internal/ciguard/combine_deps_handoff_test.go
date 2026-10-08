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
