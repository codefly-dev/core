package ciguard

import (
	"gopkg.in/yaml.v3"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// The parser and evaluator in expression_test.go decide every condition and
// every secret reference the guards assert about, so a shape they read wrongly
// is an escape hatch for all of them at once. These are the shapes that matter,
// including each one a review has required.

// theRepository and theDefaultBranch are facts about this repository, not about
// any event.
const (
	theRepository    = "codefly-dev/core"
	theDefaultBranch = "main"
)

// pullRequest binds ONLY what the event itself determines. Not the ref, not the
// head branch, not the actor -- a pull request's author chooses those, and a
// condition comparing one of them to a literal must therefore read as UNKNOWN
// rather than as false. Three bindings are enough to make a push-gated
// condition provably false, which is the only sound way to be false here.
var pullRequest = scenario{
	name: "a pull request",
	fixed: map[string]string{
		"github.event_name":                      "pull_request",
		"github.repository":                      theRepository,
		"github.event.repository.default_branch": theDefaultBranch,
	},
	prefixed: map[string]string{
		// GitHub forces `refs/pull/<n>/merge`; the number is the author's. As a
		// DOMAIN this is sound and still useful: `== 'refs/heads/main'` is
		// outside it and therefore false, while `== 'refs/pull/8/merge'` is
		// inside it and therefore unknown -- which is the right answer, since
		// on pull request 8 it is true.
		"github.ref": "refs/pull/",
	},
	always: map[string]tri{"always": triTrue},
}

// mergeGroup is the other way a pull request's code runs: a merge queue builds
// the candidate commits, which are not on the default branch yet.
var mergeGroup = scenario{
	name: "a merge queue candidate",
	fixed: map[string]string{
		"github.event_name":                      "merge_group",
		"github.repository":                      theRepository,
		"github.event.repository.default_branch": theDefaultBranch,
	},
	always: map[string]tri{"always": triTrue},
}

// workflowRunFromAPullRequest and workflowRunFromAForkPush are the two hostile
// shapes a workflow_run can take, and they are separate on purpose: each is
// stopped by a DIFFERENT condition, so a job guarded on only one of the two
// still fails against the other.
//
// Each binds ONE fact: the hostile one. Nothing else -- and in particular
// nothing the triggering party chooses.
//
// Pinning the "favourable" fields so that exactly one condition was left to do
// the work is what these scenarios did before, and it was unsound twice over.
// `conclusion` was pinned to "success", so `conclusion == 'failure'` read as
// definitely-false and a write-token job gated on it was certified -- while an
// triggering party arranges their own upstream run. `github.repository` was
// pinned to this repository, so `github.repository != 'codefly-dev/core'` read
// as definitely-false for every caller, although a reusable workflow is given
// the CALLER's context. The pins were never needed for the verdict either:
// unknown AND false is false, so an unbound favourable field cannot stop the
// hostile condition from refusing the job. A sample is a regression example,
// never a universal exclusion.
var workflowRunFromAPullRequest = scenario{
	name: "a workflow_run produced by a pull request",
	fixed: map[string]string{
		// A workflow_run fires on the repository that OWNS the workflow, so
		// these two are facts about this repository, not values the
		// triggering party chooses. `head_repository.full_name ==
		// github.repository` is a real comparison and it needs both sides.
		"github.event_name":                      "workflow_run",
		"github.repository":                      theRepository,
		"github.event.repository.default_branch": theDefaultBranch,
	},
	adversarial: map[string]string{
		// The hostile fact, and the ONLY one: the upstream run came from a
		// pull request.
		"github.event.workflow_run.event": "pull_request",
	},
	always: map[string]tri{"always": triTrue},
}

var workflowRunFromAForkPush = scenario{
	name: "a workflow_run produced by a push to a fork",
	fixed: map[string]string{
		"github.event_name":                      "workflow_run",
		"github.repository":                      theRepository,
		"github.event.repository.default_branch": theDefaultBranch,
	},
	excluded: map[string][]string{
		// The hostile fact, as a RELATION rather than a sample: the head
		// repository is any repository except this one. Pinning the literal
		// `a-contributor/core` described one fork, so a condition naming a
		// third repository was definitely-false under this scenario and under
		// the pull-request one, and the job was accepted.
		"github.event.workflow_run.head_repository.full_name": {theRepository},
	},
	// Nothing else. `event` and `conclusion` were pinned here to favourable
	// values so that one clause did the work, and that made
	// `workflow_run.event == 'schedule'` and `conclusion == 'failure'` read as
	// definitely-false -- a job gated on either was accepted. The separation
	// those pins were for comes from the two scenarios between them instead:
	// each premise refutes one clause and leaves the other unknown, so a job
	// carrying only one clause is refused by the other scenario.
	always: map[string]tri{"always": triTrue},
}

// A reusable workflow is called by another repository, which supplies its own
// context: its repository, its ref, its actor -- and its EVENT. The event is
// the one fact a condition can still use, because `github.event_name` in a
// called workflow is the caller's triggering event, so
// `github.event_name == 'push'` is genuinely false when the caller came from
// a pull request, however little else is knowable. Modelling the caller as
// "nothing is known" throws that away and fails a correct design; modelling
// it as this repository's own event (which it is not) is the unsound
// direction, and is the hole the reviewer named: `github.repository` pinned
// here read as definitely-equal for every caller, so
// `github.repository != 'codefly-dev/core'` excluded nobody.
//
// So: one scenario per HOSTILE CALLER EVENT, each binding the event and
// nothing else.
var calledFromAPullRequest = scenario{
	name:   "called as a reusable workflow from a pull request",
	fixed:  map[string]string{"github.event_name": "pull_request"},
	always: map[string]tri{"always": triTrue},
}

var calledFromACommentOnAPullRequest = scenario{
	name:   "called as a reusable workflow from a comment on a pull request",
	fixed:  map[string]string{"github.event_name": "issue_comment"},
	always: map[string]tri{"always": triTrue},
}

var calledFromAMergeQueue = scenario{
	name:   "called as a reusable workflow from a merge queue candidate",
	fixed:  map[string]string{"github.event_name": "merge_group"},
	always: map[string]tri{"always": triTrue},
}

// commentOnAPullRequest is the trigger the classifier called hostile and the
// assertion never evaluated. `issue_comment` fires on a comment -- including
// one on a pull request, where `github.event.issue.number` names that pull
// request and a checkout can select `refs/pull/<n>/head`, i.e. the author's
// unreviewed code. A credential-bearing job gated on
// `github.event_name == 'issue_comment'` was accepted because the only
// scenarios evaluated were a pull request and a merge queue candidate, under
// both of which that condition is false.
var commentOnAPullRequest = scenario{
	name: "a comment on a pull request",
	fixed: map[string]string{
		"github.event_name":                      "issue_comment",
		"github.repository":                      theRepository,
		"github.event.repository.default_branch": theDefaultBranch,
	},
	always: map[string]tri{"always": triTrue},
}

// hostileScenariosFor is the set a credential-bearing job in this workflow
// must be proven unreachable under, DERIVED from the triggers the workflow
// declares rather than fixed in the assertion.
//
// This is the finding that made the derivation necessary: the classifier
// called `issue_comment` hostile, the assertion evaluated a pull request and
// a merge queue candidate, and a job gated on `issue_comment` was false under
// both and therefore accepted. Meanwhile `workflow_run` was exempt HERE
// because it "has its own stricter guard", and that guard looked only at
// write-CAPABLE jobs -- so a read-only job holding a repository secret on
// `workflow_run` was checked by neither.
//
// An exemption is only as good as the covering guard's SCOPE, so each one
// below names its guard and why that guard's scope is enough. A declared
// trigger with no scenario is not skipped: it comes back as unmodelled and
// the caller fails on it, because "added by GitHub next year" must mean a
// failing test rather than a silent pass.
func hostileScenariosFor(on yaml.Node) (hostile []scenario, unmodelled []string) {
	for _, trigger := range triggers(on) {
		hostile = append(hostile, scenariosForTrigger(trigger)...)
	}
	return hostile, nil
}

// scenariosForTrigger builds the hostile situations a trigger admits, BINDING
// THE EVENT IT IS A SCENARIO OF.
//
// The previous version mapped triggers onto a handful of hand-written
// scenarios: `pull_request_target` was evaluated as `pull_request`, and
// `discussion_comment`, `pull_request_review` and `pull_request_review_comment`
// as `issue_comment`. Substituting one event for another is not derivation --
// a condition naming its own event (`github.event_name ==
// 'pull_request_target'`) then reads as definitely FALSE, and a job holding a
// secret and checking out `github.event.pull_request.head.sha` was accepted.
//
// It also exempted `push`, `create`, `delete`, `release`, `schedule` and
// `workflow_dispatch` with comments claiming another guard covered them. Each
// had a concrete accepted construction, because trigger identity cannot
// establish WHICH CODE a job executes. There are no exemptions here now: every
// trigger yields a scenario, and a credential-bearing job answers for itself
// either by being provably unreachable or by proving what it runs (see
// acceptedExecution).
//
// Built rather than listed, so a trigger GitHub ships next year is covered
// before anyone notices it exists: the default binds only the event's own name
// and leaves every other fact unknown.
func scenariosForTrigger(trigger string) []scenario {
	switch trigger {
	case "pull_request":
		// GitHub forces `refs/pull/<n>/merge`; the number is the author's.
		return []scenario{eventScenario(trigger, "refs/pull/", nil)}

	case "pull_request_target":
		// This event runs with the BASE repository's privileges and supplies a
		// BASE BRANCH ref -- the ref looks entirely trustworthy and says
		// nothing about the code, which is the pull request's. Giving it a
		// `refs/pull/` domain certified ordinary branch-ref conditions as
		// unreachable, so a job holding a secret and checking out the pull
		// request's head was accepted. The ref is a branch here, and what
		// protects such a job is the execution check, never the ref.
		return []scenario{eventScenario(trigger, "refs/heads/", nil)}

	case "merge_group":
		return []scenario{eventScenario(trigger, "refs/heads/gh-readonly-queue/", nil)}

	case "push", "create", "delete":
		// A ref event, but NOT necessarily the default branch: a branch or tag
		// can be created at any commit, a pull request's head included. So the
		// hostile shape is "a ref that is not the default branch", which only a
		// relational constraint can express.
		return []scenario{eventScenario(trigger, "refs/", []string{defaultBranchRef})}

	case "workflow_run":
		return []scenario{workflowRunFromAPullRequest, workflowRunFromAForkPush}

	case "workflow_call":
		// The caller's event is the caller's choice, and this repository cannot
		// see it. Every event above is possible, AND so is one nobody listed --
		// the remainder, which binds no event name at all, so any comparison
		// against one is unknown. A called job therefore cannot be accepted on
		// its condition alone; it proves what it executes instead.
		return callerScenarios()

	default:
		// Including `schedule`, `release` and `workflow_dispatch`, which used
		// to be exempt. A schedule does run the default branch's workflow, and
		// a dispatcher does need write access -- neither fact constrains the
		// ref a job then CHECKS OUT, which is the only question that matters.
		return []scenario{eventScenario(trigger, "", []string{})}
	}
}

// defaultBranchRef is the one ref a hostile ref-event scenario excludes.
const defaultBranchRef = "refs/heads/" + theDefaultBranch

// eventScenario is the default shape: the event's own name, this repository,
// its default branch, and NOTHING else. `refPrefix` and `refExcluded` describe
// the ref where the event constrains it; absent both, the ref is unknown.
func eventScenario(eventName, refPrefix string, refExcluded []string) scenario {
	s := scenario{
		name: "a " + eventName + " event",
		fixed: map[string]string{
			"github.event_name":                      eventName,
			"github.repository":                      theRepository,
			"github.event.repository.default_branch": theDefaultBranch,
		},
		always: map[string]tri{"always": triTrue},
	}
	if refPrefix != "" {
		s.prefixed = map[string]string{"github.ref": refPrefix}
	}
	if len(refExcluded) > 0 {
		s.excluded = map[string][]string{"github.ref": refExcluded}
	}
	return s
}

// callerEvents are the events a reusable workflow's caller may be running
// under. The list is for COVERAGE, not for closure: callerScenarios always
// appends the remainder, so a caller event missing from it is still modelled.
var callerEvents = []string{
	"pull_request", "pull_request_target", "merge_group",
	"issue_comment", "discussion_comment",
	"pull_request_review", "pull_request_review_comment",
	"push", "create", "delete", "release",
	"schedule", "workflow_dispatch", "repository_dispatch",
}

// callerScenarios is every caller event plus the unknown remainder.
//
// The remainder is the part that matters. Checking three listed caller events
// let a called credential job gated on `github.event_name ==
// 'workflow_dispatch'` pass all three, while a caller dispatching against an
// unmerged branch reached its bare checkout. With the remainder present no
// comparison against an event name can be refuted, so such a job must prove
// what it executes.
func callerScenarios() []scenario {
	out := make([]scenario, 0, len(callerEvents)+1)
	for _, event := range callerEvents {
		for _, s := range scenariosForTrigger(event) {
			s.name = "a caller running " + s.name
			out = append(out, s)
		}
	}
	// The remainder: this repository and its default branch are still facts,
	// but the caller's event, ref and ownership are not.
	out = append(out, scenario{
		name:   "a caller running an event this package does not enumerate",
		always: map[string]tri{"always": triTrue},
	})
	return out
}

// pushOfATag is not hostile by itself -- it is how a release happens -- but a
// tag can be pushed to any commit, so reachability under it is what obliges a
// job to prove the commit is on the default branch.
var pushOfATag = scenario{
	name: "a pushed tag",
	fixed: map[string]string{
		"github.event_name": "push",
		// NOT `github.repository`. A reusable workflow runs in the CALLER's
		// repository, so pinning this one made
		// `github.repository != 'codefly-dev/core'` definitely-false and the
		// tag-reachability question answered "no" for a caller's tag -- which
		// skipped the ancestry proof for exactly the job that needed it.
		"github.event.repository.default_branch": theDefaultBranch,
	},
	prefixed: map[string]string{
		// A tag push fixes the prefix and nothing else: the tag name is chosen.
		// As a domain, `startsWith(github.ref, 'refs/tags/')` is TRUE for every
		// tag, `== 'refs/heads/main'` is FALSE, and `== 'refs/tags/v1.0.0'` is
		// UNKNOWN -- so a job gated on one exact tag name is still asked for an
		// ancestry proof, which the earlier sampled binding let through.
		"github.ref": "refs/tags/",
	},
	always: map[string]tri{"always": triTrue},
}

// everyScenario is what the construction tests below sweep.
var everyScenario = []scenario{
	pullRequest, mergeGroup,
	workflowRunFromAPullRequest, workflowRunFromAForkPush,
	pushOfATag,
}

// chosenByTheTriggeringParty are context paths whose value is picked by whoever
// triggered the run. Binding one of them in `fixed` is unsound: a comparison
// against any other literal then reads as definitely-false, and a
// credential-bearing job gated on it is accepted while being perfectly
// reachable.
//
// This is the construction. The hole it closes was live -- `github.head_ref`
// and `github.event.workflow_run.head_branch` were both bound -- and it was
// invisible in every passing test, because a wrong binding makes guards pass,
// never fail.
var chosenByTheTriggeringParty = []string{
	"github.head_ref",
	"github.actor",
	"github.triggering_actor",
	"github.event.pull_request.head.ref",
	"github.event.pull_request.head.repo.full_name",
	"github.event.pull_request.title",
	"github.event.pull_request.body",
	"github.event.workflow_run.head_branch",
	"github.event.workflow_run.head_sha",
	"github.event.workflow_run.head_commit.message",
	"github.event.workflow_run.head_repository.full_name",
	"github.event.workflow_run.event",
	"github.event.workflow_run.conclusion",
	"github.ref",
	"github.ref_name",
}

func TestNoScenarioBindsAValueTheTriggeringPartyChoosesAsIfItWereFixed(t *testing.T) {
	for _, s := range everyScenario {
		for _, path := range chosenByTheTriggeringParty {
			require.NotContains(t, s.fixed, path,
				"scenario %q binds %s in `fixed`, but the triggering party chooses "+
					"that value. One binding then answers for one instance: a "+
					"condition comparing it to any other literal reads as "+
					"definitely-false, and a credential-bearing job gated on it is "+
					"ACCEPTED while remaining reachable. If this scenario needs the "+
					"value pinned to oblige a particular condition, put it in "+
					"`adversarial` with the reason.",
				s.name, path)
		}
	}
}

// And the consequence, asserted directly: a condition on a value the triggering
// party chooses can never be judged safe.
func TestAConditionOnAChosenValueIsNeverJudgedSafe(t *testing.T) {
	for _, s := range everyScenario {
		for _, path := range chosenByTheTriggeringParty {
			if _, pinned := s.adversarial[path]; pinned {
				continue // deliberately pinned to oblige a condition; see the scenario
			}
			// A domain-bound path is answerable only within its domain, so the
			// literal here is chosen to sit inside it.
			gate := path + " == 'release'"
			if domain, ok := s.prefixed[path]; ok {
				gate = path + " == '" + domain + "release'"
			}
			got, err := canRunUnder(gate, s)
			require.NoError(t, err)
			require.NotEqual(t, triFalse.String(), got.String(),
				"scenario %q judged %q as definitely false. A job gated on it would "+
					"be accepted, yet whoever triggers the run picks that value.",
				s.name, gate)
		}
	}
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
			// UNKNOWN under the fork's push, not true: the upstream event is
			// the triggering party's to choose and is therefore unbound. Either way
			// the job is not proven unreachable, which is the verdict that
			// matters -- but the reason is now "this guard cannot know"
			// rather than a sample that happened to favour one instance.
			name: "the event condition alone", gate: eventOnly,
			fromPullRequest: triFalse, forkPush: triUnknown,
		},
		{
			name: "the head-repository condition alone", gate: repoOnly,
			fromPullRequest: triUnknown, forkPush: triFalse,
		},
		{
			// The property that obliges version-tag.yml, and it survives the
			// unbinding: false && unknown is false from a pull request,
			// unknown && false is false from a fork's push. A conjunction is
			// definitely false as soon as ONE conjunct is, which is why the
			// favourable pins were never needed to reach this verdict.
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

// The three bypasses of the first evaluator, each a complete workflow mutation
// that actionlint accepts and each certifying a pull-request-reachable job that
// holds a secret. They are here as expression cases because that is where the
// defect was; TestTheThreeEvaluatorBypassesAreRefusedOnTheRealWorkflow applies
// them to a real job.
func TestGitHubOperatorAndCoercionSemanticsAreImplemented(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate string
		want tri
	}{
		// 1 — `&&` and `||` return the SELECTED OPERAND, not a boolean. Read as
		// booleans this compares `true` with `'run'` and is false; read as
		// GitHub reads it, the `&&` selects `'run'` and the comparison holds.
		{
			name: "operand selection through && and ||",
			gate: "(github.event_name == 'pull_request' && 'run' || '') == 'run'",
			want: triTrue,
		},
		{
			name: "the same shape with the operands reversed",
			gate: "(github.event_name == 'push' && 'run' || 'skip') == 'skip'",
			want: triTrue,
		},
		{
			name: "a selected operand's truthiness still drives a bare condition",
			gate: "github.event_name == 'push' && 'run'",
			want: triFalse,
		},
		{
			name: "an unknown left operand beside a falsy right is still falsy",
			gate: "needs.build.result == 'success' && ''",
			want: triFalse,
		},
		{
			name: "an unknown left operand beside a truthy right is truthy under ||",
			gate: "needs.build.result == 'success' || 'yes'",
			want: triTrue,
		},

		// 2 — cross-type coercion. GitHub converts both operands to numbers
		// when their types differ, so these hold there and Go's `==` on `any`
		// reports every one of them false.
		{name: "an empty string equals zero", gate: "'' == 0", want: triTrue},
		{name: "false equals zero", gate: "false == 0", want: triTrue},
		{name: "true equals one", gate: "true == 1", want: triTrue},
		{name: "null equals zero", gate: "null == 0", want: triTrue},
		{name: "a numeric string equals its number", gate: "'1' == 1", want: triTrue},
		{name: "a non-numeric string is NaN and equals nothing", gate: "'abc' == 0", want: triFalse},
		{name: "strings compare case-insensitively", gate: "'PUSH' == 'push'", want: triTrue},
		{name: "and != follows the same coercion", gate: "'' != 0", want: triFalse},

		// 3 — a ref is a domain the event fixes, not a sample. Comparing to
		// another pull request's ref must be UNKNOWN (it is true on that pull
		// request), while comparing to a branch ref is outside the domain and
		// therefore false.
		{name: "another pull request's ref is not refutable", gate: "github.ref == 'refs/pull/8/merge'", want: triUnknown},
		{name: "a branch ref is outside the domain", gate: "github.ref == 'refs/heads/main'", want: triFalse},
		{name: "a tag ref is outside the domain", gate: "startsWith(github.ref, 'refs/tags/')", want: triFalse},
		{name: "the domain itself holds", gate: "startsWith(github.ref, 'refs/pull/')", want: triTrue},
		{name: "a longer prefix inside the domain is not refutable", gate: "startsWith(github.ref, 'refs/pull/8')", want: triUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canRunUnder(tc.gate, pullRequest)
			require.NoError(t, err)
			require.Equal(t, tc.want.String(), got.String(), "condition %q", tc.gate)
		})
	}
}

// A tag push reads its ref as a domain too, which is what keeps the ancestry
// demand both correct and narrow.
func TestATagRefIsADomainNotASample(t *testing.T) {
	for _, tc := range []struct {
		gate string
		want tri
	}{
		{gate: "startsWith(github.ref, 'refs/tags/')", want: triTrue},
		{gate: "github.ref == 'refs/heads/main'", want: triFalse},
		// Was definitely-false under a sampled `refs/tags/v9.9.9`, which let a
		// job gated on one exact tag name skip the ancestry proof.
		{gate: "github.ref == 'refs/tags/v1.0.0'", want: triUnknown},
	} {
		t.Run(tc.gate, func(t *testing.T) {
			got, err := canRunUnder(tc.gate, pushOfATag)
			require.NoError(t, err)
			require.Equal(t, tc.want.String(), got.String())
		})
	}
}
