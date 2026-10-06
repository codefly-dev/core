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

// The release refusal decides whether a tag may be published with a
// credential, and it is shell in a YAML string. Reading it establishes nothing
// about what it does, so it is lifted out of the workflow and executed against
// real repositories -- the same treatment the tag-selection refusal gets in
// version_tag_selection_test.go.
//
// The invariant: a release is admitted only for a commit reachable from the
// repository's OWN default branch. Which branch that is must be the
// repository's answer; a caller may state an expectation, and a statement that
// disagrees with the repository refuses.

const releaseRefusalStep = "require the tag to be on the default branch, or refuse"

// releaseRefusalScript lifts the refusal out of go-service-release.yml.
func releaseRefusalScript(t *testing.T) string {
	t.Helper()

	_, workflows := loadIsolatedWorkflows(t)
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go-service-release.yml")
	wf, ok := workflows[path]
	require.True(t, ok, "go-service-release.yml did not load")

	for _, id := range isolatedJobIDs(wf) {
		for _, step := range wf.Jobs[id].Steps {
			if step.Name == releaseRefusalStep {
				require.NotEmpty(t, step.Run)
				return step.Run
			}
		}
	}
	t.Fatalf("no step named %q, so the refusal this test exercises is not the one that runs",
		releaseRefusalStep)
	return ""
}

// releaseFixture is a repository with a default branch, an unmerged commit, and
// two further branch names that commit also sits on -- the shape a caller would
// use to nominate its own history as the authority.
type releaseFixture struct {
	dir                  string
	tip, older, unmerged string
	defaultBranch        string
	otherBranches        []string
}

func newReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()

	base := newSelectionFixture(t, theDefaultBranch)
	git := func(args ...string) string {
		out, err := testgit.Run(context.Background(), base.dir, nil, args...)
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}

	fixture := releaseFixture{
		dir: base.dir, tip: base.tip, older: base.older,
		unmerged: base.sideBranch, defaultBranch: theDefaultBranch,
		otherBranches: []string{"topic/one", "release-next"},
	}
	for _, branch := range fixture.otherBranches {
		git("branch", branch, fixture.unmerged)
	}

	// An origin whose HEAD is the default branch: that is the authority the
	// refusal must read, rather than believing what it is told.
	origin := filepath.Join(t.TempDir(), "origin.git")
	git("clone", "--quiet", "--bare", base.dir, origin)
	git("remote", "add", "origin", origin)
	return fixture
}

// run executes the refusal with a declared branch and a selected commit.
func (f releaseFixture) run(t *testing.T, script, _ignoredDeclaration, sha string) (bool, string) {
	t.Helper()

	out, err := testgit.Run(context.Background(), f.dir, nil, "checkout", "--detach", sha)
	require.NoError(t, err, "%s", out)

	command := exec.Command("bash", "-c", script)
	command.Dir = f.dir
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GITHUB_SHA="+sha,
		"GITHUB_REF_NAME=v1.2.3",
	)
	combined, err := command.CombinedOutput()
	return err == nil, string(combined)
}

func TestAReleaseIsAdmittedOnlyFromTheRepositorysOwnDefaultBranch(t *testing.T) {
	script := releaseRefusalScript(t)

	for _, tc := range []struct {
		name     string
		declared string
		commit   string
		admit    bool
	}{
		{name: "the default branch's tip", declared: theDefaultBranch, commit: "tip", admit: true},
		{name: "an older commit on the default branch", declared: theDefaultBranch, commit: "older", admit: true},
		{name: "a commit on no branch", declared: theDefaultBranch, commit: "unmerged", admit: false},

		// The finding. A caller naming its own branch as the authority must not
		// be able to admit a commit that was never merged, however real that
		// branch is -- the commit genuinely is reachable from `topic/one`, and
		// `topic/one` is genuinely a branch. It is simply not this
		// repository's default branch.
		// There is no declaration to nominate, omit or misspell: the input was
		// removed, so the authority has exactly one source.
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newReleaseFixture(t)
			sha := map[string]string{
				"tip": fixture.tip, "older": fixture.older, "unmerged": fixture.unmerged,
			}[tc.commit]

			admitted, output := fixture.run(t, script, tc.declared, sha)
			require.Equal(t, tc.admit, admitted,
				"commit %s:\n%s", tc.commit, output)
			if !tc.admit {
				require.Contains(t, output, "refusing to release",
					"a refusal must say so, with the reason")
			}
		})
	}
}

// And the refusal has to be the thing that decides: neutralising it must be
// visible here, which is what makes this a test of behaviour rather than of
// text.
func TestNeutralisingTheReleaseRefusalIsVisible(t *testing.T) {
	script := releaseRefusalScript(t)
	fixture := newReleaseFixture(t)

	admitted, output := fixture.run(t, script, theDefaultBranch, fixture.unmerged)
	require.False(t, admitted, "%s", output)

	// The same script with its refusal swallowed admits the unmerged commit,
	// so the assertion above is load-bearing rather than incidental.
	neutralised := strings.ReplaceAll(script, "exit 1", "exit 0")
	admitted, _ = fixture.run(t, neutralised, theDefaultBranch, fixture.unmerged)
	require.True(t, admitted,
		"with every refusal turned into a success the unmerged commit is still "+
			"rejected, which means something other than the refusal decided it and "+
			"the test above proves nothing")
}

// R707-10's other half: the shell is only as trustworthy as the environment the
// YAML renders into it. A test that supplies that environment by hand proves
// nothing about the wiring, so the wiring is asserted directly -- the refusal
// step must take NO caller-supplied input at all, now that the authority has a
// single source.
func TestTheReleaseRefusalTakesNoCallerSuppliedInput(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go-service-release.yml")
	wf := workflows[path]

	var found bool
	for _, step := range wf.Jobs["goreleaser"].Steps {
		if step.Name != releaseRefusalStep {
			continue
		}
		found = true
		for name, value := range step.Env {
			require.False(t, partyChosen(t, value),
				"the refusal receives %s=%s, so the caller can influence the "+
					"authority it checks against", name, value)
		}
		require.NotContains(t, step.Run, "DECLARED_BRANCH",
			"the refusal still reads a declaration; the authority has one source")
	}
	require.True(t, found, "no step named %q", releaseRefusalStep)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "default-branch",
		"the workflow still declares a default-branch input, which can be "+
			"omitted, defaulted or disagreed with")
}
