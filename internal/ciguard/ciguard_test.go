// Package ciguard asserts invariants about this repository's CI configuration
// that nothing else in the test suite would notice.
//
// These are not tests of shipped code. They exist because a broken workflow or
// dependabot entry fails in a place no Go test looks: a weekly Dependabot pull
// request that is red for a reason unrelated to its own diff, or a dependency
// that is silently never updated because no entry covers its manifest.
package ciguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "go.mod"))
	return dir
}

type workflow struct {
	On   any `yaml:"on"`
	Jobs map[string]struct {
		Steps []struct {
			Name string            `yaml:"name"`
			Uses string            `yaml:"uses"`
			If   string            `yaml:"if"`
			Env  map[string]string `yaml:"env"`
			With map[string]any    `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func workflowFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no workflows found")
	return paths
}

// triggers normalises the `on:` key, which YAML may present as a string, a
// list, or a map depending on how the workflow is written.
func triggers(on any) []string {
	switch v := on.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(v))
		for k := range v {
			out = append(out, k)
		}
		return out
	}
	return nil
}

var secretRef = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`)

// A workflow run triggered by Dependabot is handled as though it came from a
// fork: `secrets.*` resolves to the empty string and GITHUB_TOKEN is read-only.
// A step that needs a real Actions secret therefore FAILS on every Dependabot
// pull request, turning the run red after the real work has already passed.
//
// This caught the Slack notification in go.yml, which ran `if: success()` --
// on every green pull request -- and whose action rejects an empty webhook
// with `throw new Error('Need to provide at least one botToken or webhookUrl')`.
//
// GITHUB_TOKEN is exempt: it is always populated, just read-only.
func TestPullRequestWorkflowsDoNotRequireActionsSecrets(t *testing.T) {
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf workflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		runsOnPR := false
		for _, trigger := range triggers(wf.On) {
			if trigger == "pull_request" || trigger == "pull_request_target" {
				runsOnPR = true
			}
		}
		if !runsOnPR {
			continue
		}

		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				var refs []string
				for _, value := range step.Env {
					for _, m := range secretRef.FindAllStringSubmatch(value, -1) {
						if m[1] != "GITHUB_TOKEN" {
							refs = append(refs, m[1])
						}
					}
				}
				if len(refs) == 0 {
					continue
				}
				// The step must be unable to run on a pull_request event.
				require.Contains(t, step.If, "github.event_name == 'push'",
					"%s: job %q step %q consumes Actions secret(s) %v but is not "+
						"gated to push events. On a Dependabot pull request those "+
						"secrets are empty and the step fails, turning an otherwise "+
						"green run red.",
					filepath.Base(path), jobName, step.Name, refs)
			}
		}
	}
}

var pinnedUse = regexp.MustCompile(`^([^@\s]+)@([0-9a-f]{40})\s*#\s*(\S+)\s*$`)

// usesLine matches a step's `uses:` in either YAML form -- `- uses: x` (list
// item) or `uses: x` (under a `- name:`) -- optionally commented out. Missing
// the list-item form silently exempts most of the workflow from these checks.
var usesLine = regexp.MustCompile(`^\s*#?\s*-?\s*uses:\s*(.+?)\s*$`)

// actionRef returns the action reference on a line, if the line declares one.
func actionRef(line string) (string, bool) {
	m := usesLine.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	// Keep any trailing `# <tag>` comment: it is part of what we verify.
	return strings.TrimSpace(m[1]), true
}

// Every external action must be pinned to a full commit SHA. A mutable tag is
// repointable by whoever controls the action, so an unpinned workflow runs
// whatever that tag means on the day CI fires.
func TestEveryActionIsPinnedToAFullCommitSHA(t *testing.T) {
	seen := 0
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		for i, line := range strings.Split(string(raw), "\n") {
			ref, ok := actionRef(line)
			if !ok {
				continue
			}
			// Local composite actions and reusable workflows in this repo are
			// versioned by the commit that runs them.
			if strings.HasPrefix(ref, "./") {
				continue
			}
			seen++
			require.Regexp(t, pinnedUse, ref,
				"%s:%d is not pinned as `owner/repo@<40-char sha> # <exact tag>`",
				filepath.Base(path), i+1)
		}
	}
	require.NotZero(t, seen, "found no external action references to check")
}

// The trailing comment exists so a reader can tell what version they are on
// without resolving the SHA by hand. `# v4` against a SHA that is really
// v4.4.0 does not do that, so the comment must name the EXACT tag the SHA
// carries -- which is only true if the tag actually resolves to it upstream.
//
// Skips when the network is unavailable, matching scripts/check_version_tag.sh.
func TestPinnedActionCommentsNameTheExactUpstreamTag(t *testing.T) {
	if testing.Short() {
		t.Skip("network required")
	}

	// sha -> the tag the comment claims, per "owner/repo".
	claims := map[string]map[string]string{}
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, line := range strings.Split(string(raw), "\n") {
			ref, ok := actionRef(line)
			if !ok {
				continue
			}
			m := pinnedUse.FindStringSubmatch(ref)
			if m == nil {
				continue
			}
			// An action may live in a subdirectory of its repo.
			parts := strings.SplitN(m[1], "/", 3)
			if len(parts) < 2 {
				continue
			}
			repo := parts[0] + "/" + parts[1]
			if claims[repo] == nil {
				claims[repo] = map[string]string{}
			}
			claims[repo][m[2]] = m[3]
		}
	}
	require.NotEmpty(t, claims)

	for repo, pins := range claims {
		out, err := exec.Command("git", "ls-remote", "--tags",
			"https://github.com/"+repo).Output()
		if err != nil {
			t.Skipf("cannot reach github.com for %s: %v", repo, err)
		}

		// tag -> commit. An annotated tag yields both the tag object and a
		// dereferenced "^{}" line; the dereferenced commit is the identity.
		commitOf := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			sha, tag := fields[0], strings.TrimPrefix(fields[1], "refs/tags/")
			if deref := strings.TrimSuffix(tag, "^{}"); deref != tag {
				commitOf[deref] = sha // dereferenced wins
			} else if _, ok := commitOf[tag]; !ok {
				commitOf[tag] = sha
			}
		}

		// Invert: commit -> every tag pointing at it.
		tagsAt := map[string][]string{}
		for tag, sha := range commitOf {
			tagsAt[sha] = append(tagsAt[sha], tag)
		}

		for sha, claimed := range pins {
			candidates := tagsAt[sha]
			require.NotEmpty(t, candidates,
				"%s: no upstream tag points at pinned commit %s (comment claims %s)",
				repo, sha, claimed)

			// A moving alias (v4) and the release it currently points at
			// (v4.4.0) both resolve to the same commit, so "the tag exists and
			// matches" is not enough -- v4 will silently mean something else
			// tomorrow. Demand the most precise tag on the commit.
			want := candidates[0]
			for _, tag := range candidates[1:] {
				if morePrecise(tag, want) {
					want = tag
				}
			}
			require.Equal(t, want, claimed,
				"%s is pinned at %s, whose most precise tag is %s, but the "+
					"comment says %s. A reader cannot tell which release they "+
					"are on from a moving alias. Tags on that commit: %v",
				repo, sha, want, claimed, candidates)
		}
	}
}

// morePrecise reports whether tag a is a more specific version reference than
// b: more dot-separated components first (v4.4.0 over v4), then longer.
func morePrecise(a, b string) bool {
	ca, cb := strings.Count(a, "."), strings.Count(b, ".")
	if ca != cb {
		return ca > cb
	}
	return len(a) > len(b)
}
