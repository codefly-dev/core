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
	return parseIsolatedDocument(t, text, "a mutated workflow")
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

			found := secretsIn(t, wf, "build")
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
		found := secretsIn(t, wf, id)
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

			require.NotEmpty(t, secretsIn(t, wf, "notify"),
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

// The field-by-field walk in secretsIn names the places a credential is
// usually written. A job can carry one in at least seven others, and a guard
// that names fields exempts every field it does not name -- which is the same
// failure as a pattern that matches one spelling of a secret reference.
//
// So these are the UNMODELLED fields, each put on the job that runs the suite.
// They are reported by the backstop rather than by name, which is the honest
// answer: it says a credential is in this job and that the guard cannot say
// which field, instead of saying there is none.
func TestASecretInAFieldTheModelDoesNotNameIsStillFound(t *testing.T) {
	const jobHeader = "  build:\n    name: Build\n    runs-on: ubuntu-latest\n"

	for _, tc := range []struct {
		name     string
		injected string
	}{
		{
			name:     "a service container's env",
			injected: "    services:\n      db:\n        image: postgres\n        env:\n          PASSWORD: ${{ secrets.SLACK_WEBHOOK_URL }}\n",
		},
		{
			name:     "a job container's registry credentials",
			injected: "    container:\n      image: ghcr.io/x/y\n      credentials:\n        password: ${{ secrets.SLACK_WEBHOOK_URL }}\n",
		},
		{
			name:     "a matrix value",
			injected: "    strategy:\n      matrix:\n        token: ['${{ secrets.SLACK_WEBHOOK_URL }}']\n",
		},
		{
			name:     "a job output",
			injected: "    outputs:\n      leaked: ${{ secrets.SLACK_WEBHOOK_URL }}\n",
		},
		{
			name:     "an environment url",
			injected: "    environment:\n      name: staging\n      url: https://x/${{ secrets.SLACK_WEBHOOK_URL }}\n",
		},
		{
			name:     "a concurrency group",
			injected: "    concurrency:\n      group: g-${{ secrets.SLACK_WEBHOOK_URL }}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolated(t, mutate(t, "go.yml", jobHeader, jobHeader+tc.injected))

			found := secretsIn(t, wf, "build")
			require.Contains(t, found, "SLACK_WEBHOOK_URL",
				"a credential in %s reaches the job that runs the suite, and the "+
					"guard reported none -- the field walk does not name this field "+
					"and the backstop did not catch it either", tc.name)
			require.Contains(t, strings.Join(found["SLACK_WEBHOOK_URL"], " "),
				"does not model by name",
				"this field is not modelled, so the report must say so rather than "+
					"claiming a location it did not read")
		})
	}
}

// And the backstop must not claim a secret that is not there, or every job
// reads as credential-bearing and the guards stop distinguishing anything.
func TestTheBackstopDoesNotInventSecrets(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)

	path := filepath.Join(repoRoot(t), ".github", "workflows", "go.yml")
	wf := workflows[path]
	require.NotEmpty(t, wf.Jobs, "go.yml did not load")

	for _, id := range []string{"build", "proto", "pnpm-source-evidence", "coverage-badge"} {
		require.Empty(t, secretsIn(t, wf, id),
			"job %q names no secret anywhere, and the backstop must agree", id)
	}
	require.Contains(t, secretsIn(t, wf, "notify"), "SLACK_WEBHOOK_URL",
		"the one job that does hold a credential must still be reported")
}

// The checkout-ref guard used to look for three named payload fields. Any read
// of the triggering run's payload names code this repository did not choose, so
// these are the forms that list would have missed.
func TestAnyRefTakenFromTheTriggeringRunIsCaught(t *testing.T) {
	const anchor = "        with:\n          # No `ref:`. A workflow_run checkout defaults to the default branch,\n"

	for _, ref := range []string{
		"${{ github.event.workflow_run.head_sha }}",
		"${{ github.event.workflow_run.head_branch }}",
		// The fourth field, which the enumeration did not have.
		"${{ github.event.workflow_run.head_repository.default_branch }}",
		// Reached through a function rather than read directly.
		"${{ format('{0}', github.event.workflow_run.head_sha) }}",
		// Reached by index rather than by property.
		"${{ github.event.workflow_run['head_sha'] }}",
	} {
		t.Run(ref, func(t *testing.T) {
			text := mutate(t, "version-tag.yml", anchor, anchor+"          ref: "+ref+"\n")
			wf := parsePermissioned(t, text)

			var checked int
			for _, id := range jobIDs(wf) {
				for _, step := range wf.Jobs[id].Steps {
					value, ok := step.With["ref"].(string)
					if !ok {
						continue
					}
					require.NotEmpty(t, refsFromTheTriggeringRun(t, value),
						"a checkout ref of %q takes the triggering run's code and the "+
							"guard read it as safe", value)
					checked++
				}
			}
			require.Equal(t, 1, checked, "the mutation must add exactly one ref:")
		})
	}
}

// And a ref that is NOT from the triggering payload must not be flagged, or the
// guard stops distinguishing and every workflow_run checkout reads as unsafe.
func TestARefUnrelatedToTheTriggeringRunIsNotFlagged(t *testing.T) {
	for _, ref := range []string{
		"main",
		"${{ github.event.repository.default_branch }}",
		"${{ github.sha }}",
		"refs/heads/main",
	} {
		t.Run(ref, func(t *testing.T) {
			require.Empty(t, refsFromTheTriggeringRun(t, ref))
		})
	}
}

// The three evaluator bypasses, as the round posed them: complete workflow
// mutations that actionlint accepts, each leaving the webhook-bearing `notify`
// job reachable from a pull request while the old evaluator certified it safe.
//
// Expressed against the real job, because that is the claim that matters -- not
// that a predicate handles a string, but that this workflow is refused.
func TestTheThreeEvaluatorBypassesAreRefusedOnTheRealWorkflow(t *testing.T) {
	const anchor = "    if: always() && github.event_name == 'push' && github.ref == 'refs/heads/main'\n"

	for _, tc := range []struct {
		name      string
		condition string
	}{
		{
			// `&&` and `||` return the selected operand. Read as booleans this
			// is false; GitHub selects 'run' and runs the job.
			name:      "operand selection",
			condition: "    if: (github.event_name == 'pull_request' && 'run' || '') == 'run'\n",
		},
		{
			// Cross-type coercion: '' == 0 holds on GitHub.
			name: "cross-type coercion",
			// Wrapped, because a bare `if: '' == 0` is a quoted scalar followed
			// by more content and YAML will not read it as one string.
			condition: "    if: ${{ '' == 0 }}\n",
		},
		{
			// A sampled ref made this definitely-false; it is true on pull
			// request 8.
			name:      "another pull request's ref",
			condition: "    if: github.ref == 'refs/pull/8/merge'\n",
		},
		{
			// And the two from the previous round, kept here so all five live
			// together against the real job.
			name:      "an alternative appended",
			condition: "    if: always() && github.event_name == 'push' && github.ref == 'refs/heads/main' || true\n",
		},
		{
			name:      "the conjunction negated",
			condition: "    if: ${{ !(always() && github.event_name == 'push' && github.ref == 'refs/heads/main') }}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolated(t, mutate(t, "go.yml", anchor, tc.condition))
			notify := wf.Jobs["notify"]

			require.NotEmpty(t, secretsIn(t, wf, "notify"),
				"this regression depends on notify still holding the webhook")

			ok, reason := mustNotRunUnder(t, notify.If, pullRequest)
			require.False(t, ok,
				"condition %q leaves the webhook-bearing job reachable on a pull "+
					"request, and the guard certified it safe (%s)",
				strings.TrimSpace(tc.condition), reason)
		})
	}
}

// The trigger classification is inverted -- a trigger must be argued safe
// rather than listed as dangerous -- so these are the triggers that would have
// been exempt under the enumeration, including one GitHub has not invented.
func TestATriggerNobodyEnumeratedIsTreatedAsReachable(t *testing.T) {
	for _, trigger := range []string{
		// Both carry a pull request's context and are the classic way to reach
		// unreviewed code; neither was in the old list.
		"issue_comment",
		"pull_request_review",
		"pull_request_review_comment",
		"pull_request_target",
		"merge_group",
		// Not a real event. That is the test: a trigger this package has never
		// heard of must not be exempt.
		"some_event_github_adds_in_2027",
	} {
		t.Run(trigger, func(t *testing.T) {
			var on struct {
				On yaml.Node `yaml:"on"`
			}
			require.NoError(t, yaml.Unmarshal([]byte("on:\n  "+trigger+":\n"), &on))
			require.True(t, reachableFromAPullRequest(on.On),
				"%q is not on the list of triggers argued unable to carry a pull "+
					"request's code, so it must count as reachable. Listing the "+
					"dangerous triggers instead exempts every one nobody thought of.",
				trigger)
		})
	}
}

// And the converse: the triggers that genuinely cannot carry a pull request's
// code must not be treated as reachable, or every workflow reads as hostile and
// the guards stop distinguishing.
func TestATriggerThatCannotCarryPullRequestCodeIsNotReachable(t *testing.T) {
	for _, trigger := range triggersThatCannotCarryAPullRequestsCode {
		t.Run(trigger, func(t *testing.T) {
			var on struct {
				On yaml.Node `yaml:"on"`
			}
			require.NoError(t, yaml.Unmarshal([]byte("on:\n  "+trigger+":\n"), &on))
			require.False(t, reachableFromAPullRequest(on.On), trigger)
		})
	}
}
