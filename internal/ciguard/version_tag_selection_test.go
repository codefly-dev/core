package ciguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/testgit"
)

// TestNoWorkflowRunJobChecksOutTheTriggeringCommit establishes that the tag job
// runs the default branch's code. That shifts the whole question onto this
// step: the triggering sha is now DATA, and this is the code that decides
// whether to believe it.
//
// It is shell in a YAML string, which no compiler reads and no other test
// executes — so it is tested here, as the real script, in a real repository.
// The script is lifted out of the workflow rather than restated, because a
// restatement is a second implementation that can agree with this test while
// disagreeing with what CI runs.

const (
	versionTagWorkflow = "version-tag.yml"
	// The step is addressed by name. Renaming it without updating this fails
	// loudly, which is the point: a step this test cannot find is a step it
	// does not check.
	selectionStepName = "select the commit that passed CI, or refuse"
)

// selectionScript returns the `run:` of the tag job's selection step, and the
// branch its `env:` names. Reading the branch out of the workflow rather than
// restating it here is what makes the fixture below exercise the script against
// the branch CI actually runs it against: a rename in the workflow moves this
// test with it instead of leaving it green against a branch that is gone.
func selectionScript(t *testing.T) (script, defaultBranch string) {
	t.Helper()

	path := filepath.Join(repoRoot(t), ".github", "workflows", versionTagWorkflow)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var wf permissionedWorkflow
	require.NoError(t, yaml.Unmarshal(raw, &wf))

	for _, id := range jobIDs(wf) {
		for _, step := range wf.Jobs[id].Steps {
			if step.Name == selectionStepName {
				require.NotEmpty(t, step.Run,
					"%s: step %q carries no `run:`", versionTagWorkflow, selectionStepName)
				branch := step.Env["DEFAULT_BRANCH"]
				require.NotEmpty(t, branch,
					"%s: step %q sets no DEFAULT_BRANCH, so neither the script nor "+
						"this test can name the branch a commit has to be on",
					versionTagWorkflow, selectionStepName)
				require.NotContains(t, branch, "${{",
					"%s: step %q takes DEFAULT_BRANCH from an expression (%q). A "+
						"payload field that came back empty would make every release "+
						"refuse, silently; name the branch literally.",
					versionTagWorkflow, selectionStepName, branch)
				return step.Run, branch
			}
		}
	}
	t.Fatalf("%s has no step named %q, so the commit-selection script this test "+
		"exercises is not the one CI runs", versionTagWorkflow, selectionStepName)
	return "", ""
}

// selectionFixture is a repository shaped like the one the tag job checks out:
// a default branch with history, a refs/remotes/origin/main for the ancestry
// test to resolve, and commits that are deliberately NOT on it.
type selectionFixture struct {
	dir string
	// branch is the workflow's declared default branch, which the fixture's own
	// branch and refs/remotes/origin ref are named after.
	branch string
	// tip and older are on the default branch; sideBranch is on a branch that
	// was never merged, and dangling is a real commit object on no ref at all.
	tip, older, sideBranch, dangling string
}

func newSelectionFixture(t *testing.T, branch string) selectionFixture {
	t.Helper()

	dir := t.TempDir()
	ctx := context.Background()
	git := func(args ...string) string {
		out, err := testgit.Run(ctx, dir, nil, args...)
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}

	git("init", "--initial-branch="+branch)
	for _, message := range []string{"first", "second", "third"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "version"), []byte(message), 0o600))
		git("add", "version")
		git("commit", "-m", message)
	}

	fixture := selectionFixture{dir: dir, branch: branch}
	fixture.tip = git("rev-parse", branch)
	fixture.older = git("rev-parse", branch+"~2")

	// The tag job's checkout is `fetch-depth: 0`, which populates
	// refs/remotes/origin/*. The ancestry test resolves origin/main, so the
	// fixture has to carry one.
	git("update-ref", "refs/remotes/origin/"+branch, fixture.tip)

	// An unmerged branch: the shape of a same-repository pull request head.
	git("checkout", "-b", "unmerged")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version"), []byte("unmerged"), 0o600))
	git("add", "version")
	git("commit", "-m", "unmerged")
	fixture.sideBranch = git("rev-parse", "unmerged")
	git("checkout", branch)
	// Delete the branch but keep the object reachable from nothing, so the
	// ancestry test is the only thing that can refuse it: a commit that EXISTS
	// in the repository is the case `git cat-file` cannot catch.
	git("branch", "-D", "unmerged")
	fixture.dangling = git("commit-tree", fixture.tip+"^{tree}", "-p", fixture.tip,
		"-m", "on no ref")

	return fixture
}

// run executes the real script with SHA set, and reports whether it accepted.
func (f selectionFixture) run(t *testing.T, script, sha string) (bool, string) {
	t.Helper()

	command := exec.Command("bash", "-c", script)
	command.Dir = f.dir
	command.Env = append(os.Environ(),
		"SHA="+sha,
		"DEFAULT_BRANCH="+f.branch,
		// The fixture's commits are made by testgit; the script itself makes
		// none, but keep signing off so a developer's global config cannot
		// decide the result.
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := command.CombinedOutput()
	return err == nil, string(out)
}

// head reports the commit the fixture's working tree is on, which is how the
// accept path is checked: selecting a commit means the later steps read THAT
// commit's version file and contract manifest, not the default branch's.
func (f selectionFixture) head(t *testing.T) string {
	t.Helper()
	out, err := testgit.Run(context.Background(), f.dir, nil, "rev-parse", "HEAD")
	require.NoError(t, err, "%s", out)
	return strings.TrimSpace(string(out))
}

func TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch(t *testing.T) {
	script, branch := selectionScript(t)

	t.Run("the tip of the default branch is selected", func(t *testing.T) {
		fixture := newSelectionFixture(t, branch)
		ok, out := fixture.run(t, script, fixture.tip)
		require.True(t, ok, "the default branch's own tip was refused:\n%s", out)
		require.Equal(t, fixture.tip, fixture.head(t),
			"the script accepted the commit but left the tree elsewhere; the "+
				"version file and the contract manifest would then be read from a "+
				"different commit than the one being tagged")
	})

	t.Run("an older commit on the default branch is selected", func(t *testing.T) {
		// The release is cut from the commit that PASSED, which is routinely
		// behind the branch tip by the time the suite finishes.
		fixture := newSelectionFixture(t, branch)
		ok, out := fixture.run(t, script, fixture.older)
		require.True(t, ok, "a merged commit behind the tip was refused:\n%s", out)
		require.Equal(t, fixture.older, fixture.head(t))
	})

	t.Run("a sha that is no commit here is refused", func(t *testing.T) {
		// What a head from another repository looks like locally: the object is
		// simply absent.
		fixture := newSelectionFixture(t, branch)
		ok, out := fixture.run(t, script, strings.Repeat("dead10cc", 5))
		require.False(t, ok, "a sha absent from the repository was accepted:\n%s", out)
		require.Contains(t, out, "is not a commit in this repository")
		require.Equal(t, fixture.tip, fixture.head(t),
			"a refusal must leave the tree on the default branch")
	})

	t.Run("a commit that exists but is not on the default branch is refused", func(t *testing.T) {
		// The case the existence test cannot catch, and the one that matters:
		// unmerged code, fetched into this repository, reachable from no
		// branch. Only the ancestry test refuses it.
		fixture := newSelectionFixture(t, branch)
		ok, out := fixture.run(t, script, fixture.dangling)
		require.False(t, ok, "a commit on no branch was accepted:\n%s", out)
		require.Contains(t, out, "is not reachable from refs/remotes/origin/"+branch)
		require.Equal(t, fixture.tip, fixture.head(t))
	})

	t.Run("an empty sha is refused", func(t *testing.T) {
		// `github.event.workflow_run.head_sha` resolving to nothing must refuse,
		// not fall through to a tag on whatever is checked out. The refusal is
		// the explicit existence test rather than `set -e`: git fails, the `if
		// !` catches it, and the script exits -- which is why dropping the shell
		// options alone does not change this result.
		fixture := newSelectionFixture(t, branch)
		ok, out := fixture.run(t, script, "")
		require.False(t, ok, "an empty sha was accepted:\n%s", out)
		require.Equal(t, fixture.tip, fixture.head(t))
	})
}
