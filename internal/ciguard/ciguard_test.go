// Package ciguard asserts invariants about this repository's CI configuration
// that nothing else in the test suite would notice.
//
// These are not tests of shipped code. They exist because a broken workflow or
// dependabot entry fails in a place no Go test looks: a weekly Dependabot pull
// request that is red for a reason unrelated to its own diff, or a dependency
// that is silently never updated because no entry covers its manifest.
package ciguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	If   string            `yaml:"if"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

type workflowJob struct {
	Name string `yaml:"name"`
	If   string `yaml:"if"`
	// A job that calls a reusable workflow has no steps at all: it carries
	// `uses:` and hands its secrets over at the job level instead.
	Uses     string    `yaml:"uses"`
	Secrets  any       `yaml:"secrets"`
	Needs    yaml.Node `yaml:"needs"`
	Strategy struct {
		// Held as a node so the axes keep their DECLARATION ORDER, which is
		// the order GitHub joins matrix values into a check name.
		Matrix yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []workflowStep `yaml:"steps"`
}

// `on:` is held as a node rather than `any` so one parse serves both the
// trigger NAMES and the per-trigger filters (branches, paths, types) that
// decide whether a check is safe to require.
type workflow struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

// workflowExtensions are both spellings GitHub reads. Globbing only one of them
// exempts the other from every check in this package — including the derivation
// that decides which status checks are safe to require on `main` — and nothing
// anywhere reports the gap.
var workflowExtensions = []string{"*.yml", "*.yaml"}

// workflowPaths returns every workflow file in dir, in stable order. It is split
// out of workflowFiles so a temp directory can prove both spellings are found.
func workflowPaths(dir string) ([]string, error) {
	var out []string
	for _, pattern := range workflowExtensions {
		paths, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return nil, err
		}
		out = append(out, paths...)
	}
	sort.Strings(out)
	return out, nil
}

func workflowFiles(t *testing.T) []string {
	t.Helper()
	paths, err := workflowPaths(filepath.Join(repoRoot(t), ".github", "workflows"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no workflows found")
	return paths
}

// triggers normalises the `on:` key, which YAML may present as a string, a
// list, or a map depending on how the workflow is written.
func triggers(on yaml.Node) []string {
	switch on.Kind {
	case yaml.ScalarNode:
		return []string{on.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(on.Content))
		for _, entry := range on.Content {
			out = append(out, entry.Value)
		}
		return out
	case yaml.MappingNode:
		out := make([]string, 0, len(on.Content)/2)
		for i := 0; i+1 < len(on.Content); i += 2 {
			out = append(out, on.Content[i].Value)
		}
		return out
	}
	return nil
}

// triggerNode returns the configuration node for one trigger, when the `on:`
// key is written in mapping form and carries one.
func triggerNode(on yaml.Node, name string) (yaml.Node, bool) {
	if on.Kind != yaml.MappingNode {
		return yaml.Node{}, false
	}
	for i := 0; i+1 < len(on.Content); i += 2 {
		if on.Content[i].Value == name {
			return *on.Content[i+1], true
		}
	}
	return yaml.Node{}, false
}

var secretRef = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`)

// secretUse is one place in a workflow that consumes a real Actions secret,
// paired with the `if:` expression guarding it.
type secretUse struct {
	where string
	refs  []string
	gate  string
}

// secretRefsIn returns the secrets referenced in values. GITHUB_TOKEN is
// exempt: it is always populated, just read-only.
func secretRefsIn(values []string) []string {
	var refs []string
	for _, value := range values {
		for _, m := range secretRef.FindAllStringSubmatch(value, -1) {
			if m[1] != "GITHUB_TOKEN" {
				refs = append(refs, m[1])
			}
		}
	}
	return refs
}

// jobSecrets returns what a job hands to a reusable workflow. `inherit` passes
// every secret the caller holds, which on a Dependabot pull request is a set of
// empty strings -- so it needs the same gate an explicit one does.
func jobSecrets(secrets any) []string {
	switch s := secrets.(type) {
	case string:
		if strings.TrimSpace(s) == "inherit" {
			return []string{"inherit"}
		}
	case map[string]any:
		values := make([]string, 0, len(s))
		for _, v := range s {
			if str, ok := v.(string); ok {
				values = append(values, str)
			}
		}
		return secretRefsIn(values)
	}
	return nil
}

// secretUses returns every place in wf that needs a real Actions secret.
func secretUses(wf workflow) []secretUse {
	var out []secretUse
	for jobName, job := range wf.Jobs {
		// A job calling a reusable workflow passes secrets at the JOB level,
		// where there is no step to carry an `env:` or a `with:`.
		if refs := jobSecrets(job.Secrets); len(refs) > 0 {
			out = append(out, secretUse{
				where: fmt.Sprintf("job %q", jobName),
				refs:  refs,
				gate:  job.If,
			})
		}

		for _, step := range job.Steps {
			values := make([]string, 0, len(step.Env)+len(step.With))
			for _, value := range step.Env {
				values = append(values, value)
			}
			// A secret reaches a step just as well as an action input --
			// `with: webhook: ${{ secrets.SLACK_WEBHOOK_URL }}` is the form
			// slack-github-action's own README shows -- and is just as empty
			// on a Dependabot pull request.
			for _, value := range step.With {
				if s, ok := value.(string); ok {
					values = append(values, s)
				}
			}

			if refs := secretRefsIn(values); len(refs) > 0 {
				out = append(out, secretUse{
					where: fmt.Sprintf("job %q step %q", jobName, step.Name),
					refs:  refs,
					gate:  step.If,
				})
			}
		}
	}
	return out
}

// runsOnPullRequest reports whether a pull request can trigger wf.
func runsOnPullRequest(wf workflow) bool {
	for _, trigger := range triggers(wf.On) {
		if trigger == "pull_request" || trigger == "pull_request_target" {
			return true
		}
	}
	return false
}

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

		if !runsOnPullRequest(wf) {
			continue
		}

		for _, use := range secretUses(wf) {
			// It must be unable to run on a pull_request event.
			require.Contains(t, use.gate, "github.event_name == 'push'",
				"%s: %s consumes Actions secret(s) %v but is not gated to push "+
					"events. On a Dependabot pull request those secrets are empty "+
					"and it fails, turning an otherwise green run red.",
				filepath.Base(path), use.where, use.refs)
		}
	}
}

// validWebhookTypes are the only values slack-github-action accepts.
var validWebhookTypes = []string{"incoming-webhook", "webhook-trigger"}

// A step gated to `github.event_name == 'push'` never runs on a pull request,
// so no pull request can catch a misconfiguration inside it: the first run that
// executes the step is the merge to main. The Slack notifications are the only
// steps in that position, which is exactly how a major bump of the action
// slipped through green and then turned every push to main red on the
// notification alone -- long after build, test, race and coverage had passed.
//
// The blast radius is wider than a missing message: version-tag.yml gates on
// this workflow concluding `success`, so while the notification fails no
// release tag can be cut at all.
//
// The action resolves the webhook URL from either the `webhook` input or
// SLACK_WEBHOOK_URL in the environment, but reads the type from `webhook-type`
// alone and throws before sending when it is absent.
func TestSlackNotificationsDeclareAWebhookType(t *testing.T) {
	checked := 0
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf workflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				if !strings.HasPrefix(step.Uses, "slackapi/slack-github-action@") {
					continue
				}
				checked++
				// A literal is required on purpose: an expression resolves at
				// run time, on main, which is the one moment nothing here can
				// check.
				declared, _ := step.With["webhook-type"].(string)
				require.Contains(t, validWebhookTypes, declared,
					"%s: job %q step %q posts to a Slack webhook but its "+
						"webhook-type is %q, not one of %v written out literally. "+
						"The action fails the step with `Missing input! The "+
						"webhook type must be 'incoming-webhook' or "+
						"'webhook-trigger'` before it sends anything. Nothing on a "+
						"pull request exercises this step, so it first fails on "+
						"main -- and a red run there also stops version-tag.yml "+
						"from cutting a release tag.",
					filepath.Base(path), jobName, step.Name, declared, validWebhookTypes)

				// The same throw-before-send failure, one field over. The URL
				// resolves from the `webhook` input or SLACK_WEBHOOK_URL, so a
				// step carrying a type but no source is red on main for a
				// reason no pull request run would have surfaced either.
				require.True(t,
					step.With["webhook"] != nil || step.Env["SLACK_WEBHOOK_URL"] != "",
					"%s: job %q step %q declares a webhook-type but supplies no "+
						"webhook URL, through either the `webhook` input or "+
						"SLACK_WEBHOOK_URL in `env:`. The action fails the step "+
						"before sending anything.",
					filepath.Base(path), jobName, step.Name)
			}
		}
	}
	require.NotZero(t, checked, "found no Slack notification steps to check")
}

// GitHub accepts both `.yml` and `.yaml`. Discovery has to as well, or a
// workflow spelled the other way is exempt from every check in this package
// while appearing to be covered.
func TestWorkflowDiscoveryCoversBothYAMLExtensions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"build.yml", "notify.yaml", "notes.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("on: push\n"), 0o600))
	}

	paths, err := workflowPaths(dir)
	require.NoError(t, err)

	got := make([]string, 0, len(paths))
	for _, p := range paths {
		got = append(got, filepath.Base(p))
	}
	require.Equal(t, []string{"build.yml", "notify.yaml"}, got)
}

// The secrets guard has to see every shape that carries a secret into a job. A
// reusable-workflow call has no steps at all -- it hands secrets over at the
// job level -- so scanning steps alone passes it silently while it stays just
// as empty on a Dependabot pull request as any other secret would.
func TestSecretUsesFindsEveryShapeThatCarriesASecret(t *testing.T) {
	for _, tc := range []struct {
		name  string
		yaml  string
		where string
		refs  []string
	}{
		{
			name:  "step env",
			yaml:  "jobs:\n  a:\n    steps:\n      - name: s\n        env:\n          T: ${{ secrets.TOKEN }}\n",
			where: `job "a" step "s"`,
			refs:  []string{"TOKEN"},
		},
		{
			name:  "step with",
			yaml:  "jobs:\n  a:\n    steps:\n      - name: s\n        with:\n          token: ${{ secrets.TOKEN }}\n",
			where: `job "a" step "s"`,
			refs:  []string{"TOKEN"},
		},
		{
			name:  "job secrets inherit",
			yaml:  "jobs:\n  a:\n    uses: ./.github/workflows/r.yml\n    secrets: inherit\n",
			where: `job "a"`,
			refs:  []string{"inherit"},
		},
		{
			name:  "job secrets map",
			yaml:  "jobs:\n  a:\n    uses: ./.github/workflows/r.yml\n    secrets:\n      T: ${{ secrets.TOKEN }}\n",
			where: `job "a"`,
			refs:  []string{"TOKEN"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wf workflow
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &wf))

			uses := secretUses(wf)
			require.Len(t, uses, 1, "expected exactly one secret use, got %+v", uses)
			require.Equal(t, tc.where, uses[0].where)
			require.Equal(t, tc.refs, uses[0].refs)
		})
	}
}

// GITHUB_TOKEN is always populated on a Dependabot pull request, just
// read-only, so flagging it would make the guard cry wolf on every workflow.
func TestSecretUsesExemptsGitHubToken(t *testing.T) {
	var wf workflow
	require.NoError(t, yaml.Unmarshal([]byte(
		"jobs:\n  a:\n    steps:\n      - name: s\n        env:\n          T: ${{ secrets.GITHUB_TOKEN }}\n"), &wf))
	require.Empty(t, secretUses(wf))
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
