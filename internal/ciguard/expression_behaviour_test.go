package ciguard

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// The parser and evaluator in expression_test.go decide every condition and
// every secret reference the guards assert about, so a shape they read wrongly
// is an escape hatch for all of them at once. These are the shapes that matter,
// including each one a review has got through.

// pullRequest binds what a same-repository pull request fixes. Only these are
// bound: everything else -- `needs.*`, `success()` -- stays unknown, and a
// condition turning on an unknown is not accepted as safe.
var pullRequest = scenario{
	name: "a pull request",
	bound: map[string]string{
		"github.event_name": "pull_request",
		"github.ref":        "refs/pull/7/merge",
		"github.ref_name":   "7/merge",
		"github.head_ref":   "a-contributor-branch",
		"github.repository": "codefly-dev/core",
	},
	always: map[string]tri{"always": triTrue},
}

// workflowRunFromAPullRequest and workflowRunFromAForkPush are the two hostile
// shapes a workflow_run can take, and they are separate on purpose: each is
// stopped by a DIFFERENT condition, so a job guarded on only one of the two
// still fails against the other.
var workflowRunFromAPullRequest = scenario{
	name: "a workflow_run produced by a pull request",
	bound: map[string]string{
		"github.event_name": "workflow_run",
		"github.ref":        "refs/heads/main",
		"github.repository": "codefly-dev/core",
		// Deliberately favourable: the run succeeded, on a head branch named
		// main, in this very repository. Only the EVENT is hostile.
		"github.event.workflow_run.conclusion":                "success",
		"github.event.workflow_run.event":                     "pull_request",
		"github.event.workflow_run.head_branch":               "main",
		"github.event.workflow_run.head_repository.full_name": "codefly-dev/core",
		"github.event.repository.default_branch":              "main",
	},
	always: map[string]tri{"always": triTrue},
}

var workflowRunFromAForkPush = scenario{
	name: "a workflow_run produced by a push to a fork",
	bound: map[string]string{
		"github.event_name": "workflow_run",
		"github.ref":        "refs/heads/main",
		"github.repository": "codefly-dev/core",
		// Now the event is a push and the branch is named main; only the head
		// REPOSITORY is hostile.
		"github.event.workflow_run.conclusion":                "success",
		"github.event.workflow_run.event":                     "push",
		"github.event.workflow_run.head_branch":               "main",
		"github.event.workflow_run.head_repository.full_name": "a-contributor/core",
		"github.event.repository.default_branch":              "main",
	},
	always: map[string]tri{"always": triTrue},
}

// pushOfATag is not hostile by itself -- it is how a release happens -- but a
// tag can be pushed to any commit, so reachability under it is what obliges a
// job to prove the commit is on the default branch.
var pushOfATag = scenario{
	name: "a pushed tag",
	bound: map[string]string{
		"github.event_name": "push",
		"github.ref":        "refs/tags/v9.9.9",
		"github.ref_name":   "v9.9.9",
		"github.repository": "codefly-dev/core",
	},
	always: map[string]tri{"always": triTrue},
}

func TestAConditionIsJudgedByMeaningNotByItsText(t *testing.T) {
	const safe = "github.event_name == 'push' && github.ref == 'refs/heads/main'"

	for _, tc := range []struct {
		name string
		gate string
		want tri
	}{
		{name: "the safe form cannot run on a pull request", gate: safe, want: triFalse},
		{
			name: "with always(), as a notify job needs",
			gate: "always() && " + safe,
			want: triFalse,
		},
		{
			name: "a tag gate cannot run on a pull request",
			gate: "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')",
			want: triFalse,
		},
		{
			name: "an outcome clause it cannot know does not make it safe",
			gate: "needs.build.result == 'failure'",
			want: triUnknown,
		},
		{name: "no condition at all means always", gate: "", want: triTrue},

		// Round one's hole. Every required clause is present as text.
		{name: "|| true appended", gate: safe + " || true", want: triTrue},
		{
			name: "a second branch admitting pull requests",
			gate: safe + " || github.event_name == 'pull_request'",
			want: triTrue,
		},
		// The confirming round's hole: splitting on && reads the conjuncts and
		// misses the negation wrapped around them.
		{
			name: "the conjunction negated",
			gate: "!(always() && github.event_name == 'push' && github.ref == 'refs/heads/main' && always())",
			want: triTrue,
		},
		{
			name: "the conjunction negated, wrapped in ${{ }} as an if: may be",
			gate: "${{ !(always() && github.event_name == 'push' && github.ref == 'refs/heads/main') }}",
			want: triTrue,
		},
		{
			name: "a negation that does hold",
			gate: "!(github.event_name == 'pull_request')",
			want: triFalse,
		},
		// Only one half of a workflow_run pair, judged against the pull-request
		// shape: the event clause alone is enough here.
		{
			name: "an event clause that is satisfied is not a refusal",
			gate: "github.event_name == 'pull_request'",
			want: triTrue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canRunUnder(tc.gate, pullRequest)
			require.NoError(t, err)
			require.Equal(t, tc.want.String(), got.String(),
				"condition %q under %s", tc.gate, pullRequest.name)
		})
	}
}

// The two workflow_run shapes exist so that each required condition is obliged
// separately. A job carrying only one of them is reachable through the other.
func TestEachWorkflowRunConditionIsObligedSeparately(t *testing.T) {
	const eventOnly = "github.event.workflow_run.event == 'push'"
	const repoOnly = "github.event.workflow_run.head_repository.full_name == github.repository"

	for _, tc := range []struct {
		name                      string
		gate                      string
		fromPullRequest, forkPush tri
	}{
		{
			name: "the event condition alone", gate: eventOnly,
			fromPullRequest: triFalse, forkPush: triTrue,
		},
		{
			name: "the head-repository condition alone", gate: repoOnly,
			fromPullRequest: triTrue, forkPush: triFalse,
		},
		{
			name: "both, as version-tag carries them", gate: eventOnly + " && " + repoOnly,
			fromPullRequest: triFalse, forkPush: triFalse,
		},
		{
			name: "both, neutralised", gate: "(" + eventOnly + " && " + repoOnly + ") || true",
			fromPullRequest: triTrue, forkPush: triTrue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canRunUnder(tc.gate, workflowRunFromAPullRequest)
			require.NoError(t, err)
			require.Equal(t, tc.fromPullRequest.String(), got.String(), "from a pull request")

			got, err = canRunUnder(tc.gate, workflowRunFromAForkPush)
			require.NoError(t, err)
			require.Equal(t, tc.forkPush.String(), got.String(), "from a fork's push")
		})
	}
}

// Every form in which GitHub resolves the secrets context, because a form that
// reads as "no secret" exempts the job holding it.
func TestEverySecretsAccessFormIsRecognised(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  []string
	}{
		{name: "a property", value: "${{ secrets.SLACK_WEBHOOK_URL }}", want: []string{"SLACK_WEBHOOK_URL"}},
		{name: "an index with single quotes", value: "${{ secrets['SLACK_WEBHOOK_URL'] }}", want: []string{"SLACK_WEBHOOK_URL"}},
		{name: "an index with a doubled quote inside", value: "${{ secrets['ODD''NAME'] }}", want: []string{"ODD'NAME"}},
		{name: "the whole context to a function", value: "${{ toJSON(secrets) }}", want: []string{wholeSecretsContext}},
		{name: "a computed index names none of them", value: "${{ secrets[format('A_{0}', inputs.x)] }}", want: []string{wholeSecretsContext}},
		{name: "a property wildcard", value: "${{ secrets.* }}", want: []string{wholeSecretsContext}},
		{name: "nested in a comparison", value: "${{ secrets.A != '' && secrets['B'] != '' }}", want: []string{"A", "B"}},
		{name: "two interpolations in one value", value: "a=${{ secrets.A }} b=${{ secrets.B }}", want: []string{"A", "B"}},
		{name: "inside a run script", value: "curl -H \"Auth: ${{ secrets.TOKEN }}\" https://example.test", want: []string{"TOKEN"}},
		{name: "case-insensitive context name", value: "${{ SECRETS.A }}", want: []string{"A"}},
		{name: "no secret at all", value: "${{ github.event_name }}", want: []string{}},
		{name: "a plain string", value: "go test ./...", want: []string{}},
		{name: "a context merely NAMED secrets-ish", value: "${{ steps.secrets.outputs.x }}", want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := secretsReferencedIn(tc.value)
			require.NoError(t, err)
			sort.Strings(got)
			want := tc.want
			sort.Strings(want)
			require.Equal(t, want, got)
		})
	}
}

// An expression the parser cannot read must fail the guard that asked, not
// report nothing. Returning "no secrets found" for what it does not understand
// is exactly the failure the regex had.
func TestAnUnreadableExpressionIsAnError(t *testing.T) {
	_, err := secretsReferencedIn("${{ secrets.A && ( }}")
	require.Error(t, err, "an unparseable interpolation must be reported, not ignored")

	_, err = canRunUnder("github.event_name == ", pullRequest)
	require.Error(t, err, "an unparseable condition must be reported, not treated as false")
}

// The expressions actually in this repository's workflows all have to parse, or
// the guards are judging some of them and silently skipping others.
func TestEveryExpressionInEveryWorkflowParses(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	seen := 0
	for _, path := range paths {
		wf := workflows[path]
		raw := yamlOf(t, path)
		for _, body := range expressionsIn(raw, false) {
			_, err := parseExpression(body)
			require.NoError(t, err,
				"%s contains ${{%s}}, which this package cannot parse -- so any "+
					"secret reference or condition in it is invisible to every guard "+
					"here", path, body)
			seen++
		}
		for _, id := range isolatedJobIDs(wf) {
			_, err := canRunUnder(wf.Jobs[id].If, pullRequest)
			require.NoError(t, err, "%s: job %q has a condition this package cannot judge", path, id)
		}
	}
	require.NotZero(t, seen, "no expressions found at all, so this proves nothing")
}
