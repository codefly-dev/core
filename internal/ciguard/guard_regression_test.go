package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A guard is worth what it rejects, and the only way to know what it rejects is
// to hand it the thing it must reject. Every shape below has at some point got
// past a version of these guards; each is applied here to this repository's
// REAL workflow text, in memory, and the guard's own predicate is asked about
// the result.
//
// Applying them to the real file matters. A synthetic fixture proves the
// predicate works on a document written to exercise it; mutating `go.yml`
// proves it works on the document it actually protects, with that file's
// triggers, job names and conditions around the change.

// mutate returns a workflow's text with one replacement applied, failing if the
// anchor is not there — so a rewritten workflow turns into a loud failure here
// rather than a regression test that silently stops testing anything.
func mutate(t *testing.T, workflow, anchor, replacement string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", workflow))
	require.NoError(t, err)
	text := string(raw)
	require.Equal(t, 1, strings.Count(text, anchor),
		"%s no longer contains exactly one %q, so this regression is not "+
			"mutating what it thinks it is", workflow, anchor)
	return strings.Replace(text, anchor, replacement, 1)
}

func parseIsolated(t *testing.T, text string) isolatedWorkflow {
	t.Helper()
	var wf isolatedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(text), &wf))
	return wf
}

func parsePermissioned(t *testing.T, text string) permissionedWorkflow {
	t.Helper()
	var wf permissionedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(text), &wf))
	return wf
}

// Each of these puts a credential in `go.yml`'s `build` job -- the job that
// runs the suite from the head under review -- in a form that an earlier
// version of the secret guard reported as no secret at all.
func TestASecretSmuggledIntoTheSuiteJobIsFound(t *testing.T) {
	// Two anchors: one for the job header, where a `env:` block goes, and one
	// for the job's first step, where a new step is prepended. Injecting a
	// second `steps:` key instead would be silently dropped by the YAML parser
	// and the regression would test nothing.
	const jobHeader = "  build:\n    name: Build\n    runs-on: ubuntu-latest\n"
	const firstStep = "      - name: Pull Alpine Image\n"

	for _, tc := range []struct {
		name   string
		anchor string
		// after places the injection after the anchor (an `env:` block, which
		// belongs under the job header) rather than before it (a step, which
		// belongs ahead of the job's first one).
		after    bool
		injected string
		// expect is the name the guard must report, or wholeSecretsContext when
		// the form names no secret statically.
		expect string
	}{
		{
			name:     "an ordinary property read",
			anchor:   jobHeader,
			after:    true,
			injected: "    env:\n      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}\n",
			expect:   "SLACK_WEBHOOK_URL",
		},
		{
			name:     "read by index instead of by property",
			anchor:   jobHeader,
			after:    true,
			injected: "    env:\n      PROBE: ${{ secrets['SLACK_WEBHOOK_URL'] }}\n",
			expect:   "SLACK_WEBHOOK_URL",
		},
		{
			name:     "the whole context serialised",
			anchor:   jobHeader,
			after:    true,
			injected: "    env:\n      PROBE: ${{ toJSON(secrets) }}\n",
			expect:   wholeSecretsContext,
		},
		{
			name:     "an index computed at run time",
			anchor:   jobHeader,
			after:    true,
			injected: "    env:\n      PROBE: ${{ secrets[format('SLACK_{0}', 'WEBHOOK_URL')] }}\n",
			expect:   wholeSecretsContext,
		},
		{
			name:     "inside a step's script rather than its env",
			anchor:   firstStep,
			injected: "      - name: probe\n        run: echo ${{ secrets.SLACK_WEBHOOK_URL }}\n",
			expect:   "SLACK_WEBHOOK_URL",
		},
		{
			name:     "as an action input rather than an env var",
			anchor:   firstStep,
			injected: "      - name: probe\n        uses: some/action@v1\n        with:\n          token: ${{ secrets.SLACK_WEBHOOK_URL }}\n",
			expect:   "SLACK_WEBHOOK_URL",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replacement := tc.injected + tc.anchor
			if tc.after {
				replacement = tc.anchor + tc.injected
			}
			wf := parseIsolated(t, mutate(t, "go.yml", tc.anchor, replacement))

			found := secretsIn(t, wf, wf.Jobs["build"])
			require.Contains(t, found, tc.expect,
				"a credential in this form reaches the job that runs the suite and "+
					"the guard did not report it")

			ok, reason := mustNotRunUnder(t, wf.Jobs["build"].If, pullRequest)
			require.False(t, ok,
				"the build job must be reachable on a pull request (%s), or this "+
					"regression is not testing what it claims", reason)
		})
	}
}

// A secret at WORKFLOW level is inherited by every job, including the one
// running the suite, while appearing nowhere in that job's own text. Both
// secret guards discarded workflow env entirely.
func TestASecretInWorkflowLevelEnvIsFoundInEveryJob(t *testing.T) {
	const anchor = "permissions:\n  contents: read\n"
	text := mutate(t, "go.yml", anchor,
		"env:\n  PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}\n"+anchor)
	wf := parseIsolated(t, text)

	for _, id := range isolatedJobIDs(wf) {
		found := secretsIn(t, wf, wf.Jobs[id])
		require.Contains(t, found, "SLACK_WEBHOOK_URL",
			"job %q inherits the workflow's env, so the credential is in it", id)
		require.Contains(t, strings.Join(found["SLACK_WEBHOOK_URL"], " "), "WORKFLOW's env",
			"job %q must be told the credential is inherited, not local", id)
	}

	// And the guard as a whole must now refuse this workflow, because `build`
	// is reachable on a pull request.
	ok, _ := mustNotRunUnder(t, wf.Jobs["build"].If, pullRequest)
	require.False(t, ok)
}

// Round one's hole, in the place the confirming round found it still open: the
// write-token guard read `coverage-badge`'s condition as text, so appending
// `|| true` left every required clause present while making the job
// PR-reachable with `contents: write`.
func TestANeutralisedConditionDoesNotSatisfyTheWriteTokenGuard(t *testing.T) {
	const anchor = "    if: github.event_name == 'push' && github.ref == 'refs/heads/main'\n"

	for _, tc := range []struct {
		name      string
		condition string
	}{
		{
			name:      "an alternative appended",
			condition: "    if: github.event_name == 'push' && github.ref == 'refs/heads/main' || true\n",
		},
		{
			name:      "an alternative admitting pull requests",
			condition: "    if: github.event_name == 'push' && github.ref == 'refs/heads/main' || github.event_name == 'pull_request'\n",
		},
		{
			name:      "the conjunction negated",
			condition: "    if: ${{ !(always() && github.event_name == 'push' && github.ref == 'refs/heads/main' && always()) }}\n",
		},
		{
			name:      "the condition removed outright",
			condition: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := mutate(t, "go.yml", anchor, tc.condition)

			// The job still holds contents: write...
			permissioned := parsePermissioned(t, text)
			badge := permissioned.Jobs["coverage-badge"]
			require.True(t, writeCapable(effectivePermissions(permissioned, badge)),
				"this regression depends on coverage-badge still holding a write")

			// ...and is now reachable on a pull request, which the guard's own
			// predicate must say.
			require.False(t, cannotRunOnAPullRequest(t, badge.If),
				"condition %q leaves coverage-badge reachable on a pull request "+
					"while holding contents: write, and the guard accepted it",
				badge.If)
		})
	}
}

// The same neutralisations against the credential guard, on the job that holds
// the webhook.
func TestANeutralisedConditionDoesNotSatisfyTheCredentialGuard(t *testing.T) {
	const anchor = "    if: always() && github.event_name == 'push' && github.ref == 'refs/heads/main'\n"

	for _, condition := range []string{
		"    if: always() && github.event_name == 'push' && github.ref == 'refs/heads/main' || true\n",
		"    if: ${{ !(github.event_name == 'push' && github.ref == 'refs/heads/main') }}\n",
		"    if: always()\n",
		"",
	} {
		t.Run(strings.TrimSpace(strings.TrimPrefix(condition, "    if:"))+"|", func(t *testing.T) {
			wf := parseIsolated(t, mutate(t, "go.yml", anchor, condition))
			notify := wf.Jobs["notify"]

			require.NotEmpty(t, secretsIn(t, wf, notify),
				"this regression depends on notify still holding the webhook")
			ok, _ := mustNotRunUnder(t, notify.If, pullRequest)
			require.False(t, ok,
				"condition %q leaves the webhook reachable on a pull request and "+
					"the guard accepted it", notify.If)
		})
	}
}

// And against the workflow_run guard, where each hostile shape obliges a
// different clause: dropping either one must be caught by one of the two.
func TestDroppingEitherWorkflowRunConditionIsCaught(t *testing.T) {
	for _, tc := range []struct {
		name   string
		anchor string
	}{
		{name: "the event condition", anchor: "      github.event.workflow_run.event == 'push' &&\n"},
		{
			name:   "the head-repository condition",
			anchor: "      github.event.workflow_run.head_repository.full_name == github.repository &&\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parsePermissioned(t, mutate(t, "version-tag.yml", tc.anchor, ""))
			tag := wf.Jobs["tag"]
			require.True(t, writeCapable(effectivePermissions(wf, tag)))

			caught := false
			for _, hostile := range []scenario{workflowRunFromAPullRequest, workflowRunFromAForkPush} {
				reached, err := canRunUnder(tag.If, hostile)
				require.NoError(t, err)
				if reached != triFalse {
					caught = true
				}
			}
			require.True(t, caught,
				"with %s dropped, the tag job's condition (%q) was still judged "+
					"unreachable by both hostile workflow_run shapes",
				tc.name, tag.If)
		})
	}
}
