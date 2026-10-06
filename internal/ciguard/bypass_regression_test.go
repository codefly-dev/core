package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Every construction a review has used to get a credential into a job that
// runs code nobody merged, kept as a document the guards are run against.
//
// Each of these states an invariant these guards hold, and each is
// accepted for the same reason: a value the guard could not know was treated
// as a value it knew. They are here as documents rather than as prose so that
// a future change to the evaluator, the scenarios or the trigger derivation
// has to keep refusing them.
func TestEveryKnownCredentialBypassIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		triggers string
		gate     string
		why      string
	}{
		{
			name:     "hexadecimal coercion the evaluator does not model",
			triggers: "pull_request:\n",
			gate:     "github.event_name == 'pull_request' && '0x10' == 16",
			why:      "GitHub converts hexadecimal, so this is TRUE on a pull request; ParseFloat refuses \"0x10\" and that refusal was read as NaN, making the condition definitely false",
		},
		{
			name:     "a trigger the classifier called hostile and no scenario evaluated",
			triggers: "issue_comment:\n    types: [created]\n",
			gate:     "github.event_name == 'issue_comment'",
			why:      "the assertion evaluated a pull request and a merge queue candidate only, under both of which this is false -- while a comment on a pull request reaches it and can check out refs/pull/<n>/head",
		},
		{
			name:     "a condition about an upstream run's conclusion",
			triggers: "workflow_run:\n    workflows: [go]\n    types: [completed]\n",
			gate:     "github.event.workflow_run.conclusion == 'failure'",
			why:      "an upstream run's conclusion is the triggering party's to arrange, so a condition about it is not refutable",
		},
		{
			name:     "a negated conjunction",
			triggers: "pull_request:\n",
			gate:     "!(always() && github.event_name == 'push' && github.ref == 'refs/heads/main')",
			why:      "a negation around a conjunction is not the conjunction; this condition holds on a pull request",
		},
		{
			name:     "an operand-returning logical operator",
			triggers: "pull_request:\n",
			gate:     "(github.event_name == 'pull_request' && 'run' || '') == 'run'",
			why:      "GitHub's && and || return the selected OPERAND, so this is true on a pull request; reducing them to a boolean made it false",
		},
		{
			name:     "GitHub's cross-type equality",
			triggers: "pull_request:\n",
			gate:     "github.event_name == 'pull_request' && '' == 0",
			why:      "GitHub coerces the empty string to zero, so this is true; Go's type-sensitive equality reported false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The condition is quoted: a GitHub condition beginning with
			// "!" is a YAML tag indicator unquoted, which is why a real
			// workflow quotes it too.
			document := "name: probe\non:\n  " + tc.triggers + "\njobs:\n  probe:\n    runs-on: ubuntu-latest\n    permissions:\n      contents: read\n    if: " + quoteCondition(tc.gate) + `
    env:
      PROBE: ${{ secrets.A_REPOSITORY_SECRET }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...
`
			wf := parseIsolatedDocument(t, document, tc.name)

			secrets := secretsIn(t, wf, "probe")
			require.NotEmpty(t, secrets,
				"the probe's own secret is not seen, so this document proves nothing about the guard: %s", tc.why)

			hostile, unmodelled := hostileScenariosFor(wf.On)
			require.Empty(t, unmodelled, "the probe's trigger has no scenario")
			require.NotEmpty(t, hostile, "the probe's trigger yields no hostile scenario")

			refused := false
			for _, situation := range hostile {
				if ok, _ := mustNotRunUnder(t, wf.Jobs["probe"].If, situation); !ok {
					refused = true
				}
			}
			require.True(t, refused,
				"this condition is accepted as unreachable, and it is not: %s", tc.why)
		})
	}
}

// Every trigger yields a scenario BINDING ITS OWN EVENT, and nothing is
// exempt.
//
// This replaces a test that required an unmodelled trigger to be *reported*.
// Reporting was the right instinct for an enumeration, but the enumeration is
// gone: scenariosForTrigger has a default that binds the event's own name and
// leaves every other fact unknown, so a trigger nobody has heard of is modelled
// rather than reported. What has to be true now is stronger -- that the event
// a scenario binds is the event it is a scenario OF. Substituting one for
// another is how `pull_request_target` came to be evaluated as `pull_request`,
// and a condition naming its real event read as definitely false.
func TestEveryTriggerYieldsAScenarioBindingItsOwnEvent(t *testing.T) {
	withTrigger := func(trigger string) yaml.Node {
		wf := parseIsolatedDocument(t, "name: probe\non:\n  "+trigger+"\njobs:\n  probe:\n    runs-on: ubuntu-latest\n", trigger)
		return wf.On
	}

	for _, trigger := range []string{
		// The four that were substituted for another event.
		"pull_request_target", "discussion_comment",
		"pull_request_review", "pull_request_review_comment",
		// The six that were exempt with a comment naming another guard.
		"push", "create", "delete", "release", "schedule", "workflow_dispatch",
		// And one that does not exist.
		"some_event_github_adds_in_2027",
	} {
		t.Run(trigger, func(t *testing.T) {
			hostile, unmodelled := hostileScenariosFor(withTrigger(trigger + ":\n"))
			require.Empty(t, unmodelled)
			require.NotEmpty(t, hostile,
				"%q yielded no hostile scenario, so a credential-bearing job in such a "+
					"workflow is answerable to nothing", trigger)

			// A job gated on its OWN trigger must not be accepted as
			// unreachable. That is precisely what happened when a scenario
			// bound a different event: `pull_request_target` was evaluated as
			// `pull_request`, so `github.event_name == 'pull_request_target'`
			// read as definitely false under its only scenario.
			unreachable, _ := provablyUnreachable(t,
				"github.event_name == '"+trigger+"'", hostile)
			require.False(t, unreachable,
				"a job gated on `github.event_name == %q` is accepted as unreachable "+
					"in a workflow triggered by %q. The scenario is binding a different "+
					"event than the one it is a scenario of.", trigger, trigger)
		})
	}
}

// A reusable workflow's caller event is the caller's, and the set of them is
// open: the remainder is what makes a called job answer for what it executes
// rather than for a listed event.
func TestAReusableWorkflowModelsAnUnknownCallerEvent(t *testing.T) {
	scenarios := callerScenarios()
	require.NotEmpty(t, scenarios)

	// No caller event -- listed or not -- may make a called job provably
	// unreachable, because the caller chooses it and this repository cannot
	// see which. `workflow_dispatch` passed all three caller scenarios the
	// previous version checked, which is why the remainder closes it
	// for every event at once, including ones nobody listed.
	for _, event := range []string{
		"workflow_dispatch", "push", "release", "schedule",
		"pull_request_target", "repository_dispatch",
		"some_event_github_adds_in_2027",
	} {
		unreachable, _ := provablyUnreachable(t,
			"github.event_name == '"+event+"'", scenarios)
		require.False(t, unreachable,
			"a called job gated on `github.event_name == %q` is accepted as "+
				"unreachable, while a caller running that event reaches it", event)
	}

	// The remainder has to be in the set for that to hold at all.
	remainder := false
	for _, situation := range scenarios {
		if _, bound := situation.fixed["github.event_name"]; !bound {
			remainder = true
		}
	}
	require.True(t, remainder,
		"no scenario leaves the caller's event unbound, so the caller-event set is "+
			"closed -- and a caller running anything outside it is unmodelled")
}

// quoteCondition wraps a condition in single quotes when YAML would read its
// first character as a tag or an anchor.
func quoteCondition(gate string) string {
	if gate == "" {
		return gate
	}
	switch gate[0] {
	case '!', '&', '*', '{', '[', '>', '|', '%', '@', '`':
		return "\"" + strings.ReplaceAll(gate, "\"", "\\\"") + "\""
	}
	return gate
}

// The composite-action construction: the calling job checks out the default
// branch, hands the credential to a local composite, and the composite's own
// checkout -- with no `ref:` -- takes the triggering ref. Both the top-level
// checkout and the manifest's secrets scan passed; the nested checkout was
// seen by neither.
func TestALocalCompositeActionsStepsAreStepsOfTheCallingJob(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/actions/probe"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/actions/probe/action.yml"), []byte(`name: probe
inputs:
  token:
    required: true
runs:
  using: composite
  steps:
    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
    - run: go test ./...
      shell: bash
      env:
        PROBE: ${{ inputs.token }}
`), 0o600))

	job := isolatedJob{Steps: []isolatedStep{
		{Uses: "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", With: map[string]any{"ref": "main"}},
		{Uses: "./.github/actions/probe", With: map[string]any{"token": "${{ secrets.A_REPOSITORY_SECRET }}"}},
	}}

	steps, unreadable := stepsIncludingLocalActionsUnder(root, job)
	require.Empty(t, unreadable)

	checkouts := 0
	nestedWithoutARef := false
	for _, step := range steps {
		if step.Uses == "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1" {
			checkouts++
			if ref, _ := step.With["ref"].(string); ref == "" {
				nestedWithoutARef = true
			}
		}
	}
	require.Equal(t, 2, checkouts, "the composite's checkout is a checkout of this job")
	require.True(t, nestedWithoutARef,
		"the nested checkout takes no ref, which is the whole construction: it selects the triggering ref")

	// An action whose execution cannot be read is reported, never treated as
	// stepless -- a JavaScript action's steps are not visible, so a guard
	// that silently accepted it would exempt whatever it does.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/actions/probe/action.yml"), []byte(`name: probe
runs:
  using: node20
  main: index.js
`), 0o600))
	_, unreadable = stepsIncludingLocalActionsUnder(root, job)
	require.Len(t, unreadable, 1)
	require.Contains(t, unreadable[0], "node20")
}

// The round-seven constructions. Each is a complete workflow that actionlint
// accepts and that the guards accepted, and each must now be refused by the
// unified rule: a credential-bearing job is either provably unreachable under
// every hostile situation its triggers admit, or it proves what it executes.
//
// The four exemption constructions are here together because they were all the
// same defect: an exemption written in terms of the TRIGGER, with a comment
// naming a guard whose scope did not reach the job.
func TestTheExemptionConstructionsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workflow string
		why      string
	}{
		{
			name: "release: published with a tag at unmerged code",
			why:  "the tag proof read only push and workflow_call, so a release-triggered job was asked for nothing",
			workflow: `name: probe
on:
  release:
    types: [published]
permissions:
  contents: read
jobs:
  probe:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    env:
      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...
`,
		},
		{
			name: "push to a branch that is not the default one",
			why:  "the tag scenario proved it unreachable and no branch ancestry rule applied",
			workflow: `name: probe
on:
  push:
permissions:
  contents: read
jobs:
  probe:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' && startsWith(github.ref, 'refs/heads/')
    permissions:
      contents: read
    env:
      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...
`,
		},
		{
			name: "schedule, checking out a pull request's head explicitly",
			why:  "the workflow comes from the default branch, which says nothing about the ref a job checks out",
			workflow: `name: probe
on:
  schedule:
    - cron: '0 0 * * *'
permissions:
  contents: read
jobs:
  probe:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    env:
      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with:
          ref: refs/pull/8/head
      - run: go test ./...
`,
		},
		{
			name: "dispatch whose only credential is the built-in write token",
			why:  "the dispatch guard skipped jobs without a custom secret and the write-token guard skipped non-PR workflows",
			workflow: `name: probe
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  probe:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...
`,
		},
		{
			name: "a service image chosen by the triggering party",
			why:  "services were absent from execution analysis while the ordinary checkout stayed pinned",
			workflow: `name: probe
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  probe:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    env:
      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}
    services:
      probe:
        image: ${{ github.event.inputs.image }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with:
          ref: main
      - run: go test ./...
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, tc.workflow, tc.name)
			hostile, _ := hostileScenariosFor(wf.On)
			require.NotEmpty(t, hostile)

			credentialBearing := len(secretsIn(t, wf, "probe")) > 0
			permissioned := parsePermissioned(t, tc.workflow)
			if writeCapable(effectivePermissions(permissioned, permissioned.Jobs["probe"])) {
				credentialBearing = true
			}
			require.True(t, credentialBearing,
				"this construction depends on the job holding a credential")

			unreachable, _ := provablyUnreachable(t, wf.Jobs["probe"].If, hostile)
			if unreachable {
				t.Fatalf("accepted as unreachable, and it is not: %s", tc.why)
			}
			pinned, missing := acceptedExecution(t, wf, "probe")
			require.False(t, pinned,
				"accepted as proving what it executes, and it does not: %s (guard said: %q)",
				tc.why, missing)
		})
	}
}

// The converse, so the rule still distinguishes: a job that genuinely pins what
// it runs, or genuinely refuses before running it, is accepted.
func TestAJobThatProvesWhatItExecutesIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workflow string
	}{
		{
			name: "every checkout pinned to the default branch",
			workflow: `name: probe
on:
  workflow_dispatch:
jobs:
  probe:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    env:
      PROBE: ${{ secrets.SLACK_WEBHOOK_URL }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with:
          ref: main
      - run: bash .github/scripts/combine-deps-publish.sh out
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, tc.workflow, tc.name)
			pinned, missing := acceptedExecution(t, wf, "probe")
			require.True(t, pinned, "refused a job that does pin what it runs: %s", missing)
		})
	}
}

// A refusal counts only when a test ESTABLISHES its behaviour. Reading a script
// cannot do that, so a job cannot be accepted on one it wrote itself -- however
// the command is spelled, wherever it sits, and whatever a reader would make of
// it. The refusals that do count are registered, and each is executed against
// real repositories.
func TestARefusalNobodyExecutesIsNotARefusal(t *testing.T) {
	for _, proof := range []string{
		`      - name: require the tag to be on the default branch, or refuse
        run: git merge-base --is-ancestor "$GITHUB_SHA" "refs/remotes/origin/main"`,
		`      - name: check ancestry
        run: git merge-base --is-ancestor "$GITHUB_SHA" "refs/remotes/origin/main"`,
		`      - run: |
          set -euo pipefail
          git merge-base --is-ancestor "$GITHUB_SHA" "refs/remotes/origin/main" || exit 1`,
	} {
		t.Run(strings.TrimSpace(strings.SplitN(proof, "\n", 2)[0]), func(t *testing.T) {
			wf := parseIsolatedDocument(t, `on: workflow_call
jobs:
  probe:
    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
`+proof+`
      - run: go test ./...
`, "unregistered refusal")
			ok, missing := acceptedExecution(t, wf, "probe")
			require.False(t, ok,
				"accepted on a refusal no test executes (guard said %q). Even copying "+
					"a registered step's NAME must not work: the registry is keyed by "+
					"workflow and job, so the claim is tied to the script a test "+
					"actually runs.", missing)
		})
	}
}

// The registry cannot claim coverage that does not exist: every entry must name
// a step that is really there and a test that is really in this package.
func TestEveryVerifiedRefusalIsReallyVerified(t *testing.T) {
	require.NotEmpty(t, verifiedRefusals)

	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "ciguard", "*_test.go"))
	require.NoError(t, err)
	var all strings.Builder
	for _, source := range sources {
		body, err := os.ReadFile(source)
		require.NoError(t, err)
		all.Write(body)
	}
	tests := all.String()

	_, workflows := loadIsolatedWorkflows(t)
	for _, known := range verifiedRefusals {
		t.Run(known.workflow+"/"+known.job, func(t *testing.T) {
			path := filepath.Join(repoRoot(t), ".github", "workflows", known.workflow)
			wf, ok := workflows[path]
			require.True(t, ok, "%s does not exist", known.workflow)

			found := false
			for _, step := range wf.Jobs[known.job].Steps {
				if step.Name == known.step {
					found = true
					require.Contains(t, step.Run, "merge-base --is-ancestor",
						"the registered step no longer asks git about ancestry")
				}
			}
			require.True(t, found,
				"%s job %q has no step named %q, so this entry claims coverage of "+
					"nothing", known.workflow, known.job, known.step)

			require.Contains(t, tests, "func "+known.verifiedBy+"(",
				"the registry names %s as establishing this refusal's behaviour, and "+
					"no such test exists in this package", known.verifiedBy)
		})
	}
}

// A credential keeps its provenance through a derived object: reading a
// property of a parsed secret is still reading the secret.
func TestACredentialKeepsItsProvenanceThroughADerivedObject(t *testing.T) {
	for _, expression := range []string{
		"${{ fromJSON(secrets.BUNDLED_CONFIG).token }}",
		"${{ fromJSON(secrets['BUNDLED_CONFIG'])['token'] }}",
		"${{ fromJSON(toJSON(secrets.BUNDLED_CONFIG)).token }}",
	} {
		t.Run(expression, func(t *testing.T) {
			names, err := secretsReferencedIn(expression)
			require.NoError(t, err)
			require.Contains(t, names, "BUNDLED_CONFIG",
				"a property read off a derived object must still report the credential "+
					"it came from, or a secret reaches a job unnamed")
		})
	}
}

// The scenarios the guards actually consume must cover every head repository,
// not one sample -- asserted through the derivation entrypoint rather than the
// variables, since that is what the guards call.
func TestTheDerivedScenariosCoverEveryHeadRepository(t *testing.T) {
	for _, repository := range []string{
		"a-contributor/core", "another-owner/core", "third-party/fork",
	} {
		t.Run(repository, func(t *testing.T) {
			gate := "github.event.workflow_run.head_repository.full_name == '" + repository +
				"' && github.event.workflow_run.event == 'push'"
			unreachable, _ := provablyUnreachable(t, gate, scenariosForTrigger("workflow_run"))
			require.False(t, unreachable,
				"the derivation the guards consume judged this unreachable, so a job "+
					"gated on one named head repository would be accepted")
		})
	}
}

// A refusal has to refuse: it must execute, its failure must stop the job, and
// it must ask about the commit under consideration.
func TestANominalRefusalIsNotARefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		step string
		why  string
	}{
		{
			name: "written only in a comment",
			step: `      - run: |
          # git merge-base --is-ancestor "$GITHUB_SHA" origin/main
          echo proceeding`,
			why: "a comment runs nothing",
		},
		{
			name: "a step that may be skipped",
			step: `      - if: false
        run: git merge-base --is-ancestor "$GITHUB_SHA" origin/main`,
			why: "it proves nothing on the runs where it is skipped",
		},
		{
			name: "a failure that is swallowed",
			step: `      - run: git merge-base --is-ancestor "$GITHUB_SHA" origin/main || true`,
			why:  "a tolerated failure is not a refusal",
		},
		{
			name: "a failure the job continues past",
			step: `      - continue-on-error: true
        run: git merge-base --is-ancestor "$GITHUB_SHA" origin/main`,
			why: "same, declared on the step instead of in the shell",
		},
		{
			name: "asking about a different commit",
			step: `      - run: git merge-base --is-ancestor origin/main origin/main`,
			why:  "it never mentions the commit under consideration",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, `on: workflow_call
jobs:
  probe:
    permissions: {contents: read}
    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
`+tc.step+`
      - run: go test ./...
`, tc.name)
			ok, _ := acceptedExecution(t, wf, "probe")
			require.False(t, ok, "accepted as a proof, and it is not: %s", tc.why)
		})
	}
}

// A ref pin says which TREE is checked out. It says nothing about the program
// run from it, the repository it came from, or an image selected elsewhere.
func TestAPinnedTreeDoesNotPinWhatRunsInIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  string
		why  string
	}{
		{
			name: "the program comes from the caller",
			why:  "the tree is this repository's and the program is the caller's",
			job: `    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - env: {TASK: "${{ inputs.task }}"}
        run: eval "$TASK"`,
		},
		{
			name: "the repository comes from the caller",
			why:  "a pinned ref of somebody else's repository is not this repository",
			job: `    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main, repository: "${{ inputs.repository }}"}
      - run: go test ./...`,
		},
		{
			name: "the container is selected through a matrix",
			why:  "a matrix carries whatever its caller put in it",
			job: `    strategy:
      matrix:
        image: ['${{ inputs.image }}']
    container: ${{ matrix.image }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - run: go test ./...`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, "on: workflow_call\njobs:\n  probe:\n"+
				"    permissions: {contents: read}\n"+
				"    env: {CREDENTIAL: '${{ secrets.BUNDLED_CONFIG }}'}\n"+tc.job+"\n", tc.name)
			ok, missing := acceptedExecution(t, wf, "probe")
			require.False(t, ok,
				"accepted on a ref pin alone: %s (guard said %q)", tc.why, missing)
		})
	}
}

// And the event whose ref is trustworthy while its code is not: the ref is a
// base branch, so no branch-ref condition may be certified unreachable, and the
// execution check is what protects such a job.
func TestTheTargetEventsRefIsABranchAndProvesNothingAboutItsCode(t *testing.T) {
	for _, gate := range []string{
		"github.ref == 'refs/heads/main'",
		"startsWith(github.ref, 'refs/heads/')",
		"github['ref'] == 'refs/heads/main'",
	} {
		t.Run(gate, func(t *testing.T) {
			unreachable, _ := provablyUnreachable(t, gate, scenariosForTrigger("pull_request_target"))
			require.False(t, unreachable,
				"a branch-ref condition was certified unreachable for an event that "+
					"supplies a branch ref, so a job gated on it would be accepted")
		})
	}

	wf := parseIsolatedDocument(t, `on: pull_request_target
jobs:
  probe:
    permissions: {contents: read}
    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - run: go test ./...
`, "pull_request_target")
	ok, _ := acceptedExecution(t, wf, "probe")
	require.False(t, ok,
		"the ref is a trustworthy branch and the code is not; the execution check "+
			"is what has to catch that")
}

// Unreachability and execution are two questions, and answering the first does
// not answer the second. A job that no hostile trigger can start can still, on
// a legitimate push to the default branch, check out a different branch and run
// it with the credential — so both halves are required of every
// credential-bearing job, and this asserts the pair rather than either.
func TestUnreachabilityDoesNotExcuseWhatAJobExecutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  string
		// accepted is what the guards decide. Unreachable is true for every
		// case here, so anything that differs is the execution obligation.
		bothHold bool
	}{
		{
			name:     "unreachable, and checks out another branch",
			bothHold: false,
			job: `    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    permissions: {contents: write}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: a-contributor-branch}
      - run: go test ./...`,
		},
		{
			name:     "unreachable, and inherits whatever ref triggered it",
			bothHold: false,
			job: `    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    permissions: {contents: write}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...`,
		},
		{
			name:     "unreachable, and says which tree it wants",
			bothHold: true,
			job: `    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    permissions: {contents: write}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - run: go test ./...`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := "on:\n  push:\n  pull_request:\njobs:\n  probe:\n    runs-on: ubuntu-latest\n" + tc.job + "\n"
			wf := parseIsolatedDocument(t, document, tc.name)
			permissioned := parsePermissioned(t, document)

			require.True(t,
				writeCapable(effectivePermissions(permissioned, permissioned.Jobs["probe"])),
				"this case depends on the job holding a write token")

			hostile, _ := hostileScenariosFor(wf.On)
			unreachable, _ := provablyUnreachable(t, wf.Jobs["probe"].If, hostile)
			require.True(t, unreachable,
				"every case here is unreachable on purpose; that is the point")

			// Asked through the guards' own decision, so a short-circuit on
			// unreachability cannot hide here.
			accepted, missing := credentialJobIsAccepted(t, wf, "probe", hostile)
			require.Equal(t, tc.bothHold, accepted,
				"the job is unreachable; acceptance must still turn on what it "+
					"executes (%s)", missing)
		})
	}
}

// A pinned checkout beside an unpinned one inside a local action is not pinned:
// the nested checkout selects a tree too.
func TestANestedCheckoutMustPinTheDefaultBranchAsWell(t *testing.T) {
	root := t.TempDir()
	action := filepath.Join(root, ".github", "actions", "nested")
	require.NoError(t, os.MkdirAll(action, 0o755))

	write := func(ref string) {
		body := "name: nested\nruns:\n  using: composite\n  steps:\n" +
			"    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1\n"
		if ref != "" {
			body += "      with: {ref: " + ref + "}\n"
		}
		body += "    - run: go test ./...\n        \n"
		require.NoError(t, os.WriteFile(filepath.Join(action, "action.yml"), []byte(body), 0o600))
	}

	job := parseIsolatedDocument(t, `on: workflow_call
jobs:
  probe:
    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - uses: ./.github/actions/nested
`, "nested").Jobs["probe"]

	for _, tc := range []struct {
		name   string
		ref    string
		pinned bool
	}{
		{name: "the nested checkout names no ref", ref: "", pinned: false},
		{name: "the nested checkout names another branch", ref: "a-contributor-branch", pinned: false},
		{name: "the nested checkout names the default branch", ref: "main", pinned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(tc.ref)
			steps, unreadable := stepsIncludingLocalActionsUnder(root, job)
			require.Empty(t, unreadable)

			reason, ok := everyCheckoutPinsTheDefaultBranch(steps)
			require.Equal(t, tc.pinned, ok,
				"the outer checkout is pinned; the nested one decides (%s)", reason)
		})
	}
}

// Both refusals name the remote-tracking ref in full. A bare `origin/<branch>`
// is an ambiguous rev, and which object wins is not this repository's to decide.
func TestTheRefusalsNameTheRemoteTrackingRefInFull(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)
	require.NotEmpty(t, verifiedRefusals)

	for _, known := range verifiedRefusals {
		t.Run(known.workflow, func(t *testing.T) {
			wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", known.workflow)]
			for _, step := range wf.Jobs[known.job].Steps {
				if step.Name != known.step {
					continue
				}
				for _, line := range strings.Split(step.Run, "\n") {
					if !strings.Contains(line, "merge-base --is-ancestor") {
						continue
					}
					require.Contains(t, line, "refs/remotes/origin/",
						"the refusal compares against a bare remote name, which is an "+
							"ambiguous rev")
				}
			}
		})
	}
}

// A registry entry says a test executes the SCRIPT. These say nothing about the
// script and everything about whether the step runs: a condition on it, or a
// tolerated failure, and the job proceeds past an unmerged commit with the
// credential while the script and its test are untouched. So a step control on
// a registered refusal is itself a refusal of the witness.
func TestAControlOnARegisteredRefusalIsNotAWitness(t *testing.T) {
	for _, control := range []string{
		"        if: github.event_name != 'push'\n",
		"        if: ${{ success() }}\n",
		"        continue-on-error: true\n",
	} {
		t.Run(strings.TrimSpace(control), func(t *testing.T) {
			known := verifiedRefusals[1] // the release refusal
			path := filepath.Join(repoRoot(t), ".github", "workflows", known.workflow)
			raw, err := os.ReadFile(path)
			require.NoError(t, err)

			anchor := "      - name: " + known.step + "\n"
			require.Contains(t, string(raw), anchor)
			wf := parseIsolatedDocument(t,
				strings.Replace(string(raw), anchor, anchor+control, 1), known.workflow)
			wf.name = known.workflow

			ok, missing := acceptedExecution(t, wf, known.job)
			require.False(t, ok,
				"the registered refusal carries %q and was still accepted as a "+
					"witness (guard said %q)", strings.TrimSpace(control), missing)
			require.Contains(t, missing, "not a witness")
		})
	}
}

// A refusal establishes the commit the job began on. It establishes nothing
// about a tree selected later in the same job, or inside an action it invokes.
func TestARefusalDoesNotCoverACheckoutThatFollowsIt(t *testing.T) {
	known := verifiedRefusals[1]
	path := filepath.Join(repoRoot(t), ".github", "workflows", known.workflow)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		added string
	}{
		{
			name: "a later checkout of another branch",
			added: `      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: topic/one}
      - run: go test ./...
`,
		},
		{
			name: "a later checkout inheriting the triggering ref",
			added: `      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: go test ./...
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := "      - name: Run GoReleaser\n"
			require.Contains(t, string(raw), anchor)
			wf := parseIsolatedDocument(t,
				strings.Replace(string(raw), anchor, tc.added+anchor, 1), known.workflow)
			wf.name = known.workflow

			ok, missing := acceptedExecution(t, wf, known.job)
			require.False(t, ok,
				"a checkout after the refusal was accepted (guard said %q)", missing)
			require.Contains(t, missing, "no refusal in this job covers")
		})
	}
}

// The interpreter is `step.shell`, and which constructs run a string is a
// property of the interpreter. A shell this guard does not read refuses.
func TestTheEffectiveShellDecidesAndAnUnreadOneRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		job      string
		accepted bool
	}{
		{
			name:     "a shell whose execution forms are not read",
			accepted: false,
			job: `    env: {TASK: "${{ github.event.pull_request.body }}", CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - shell: python
        run: exec(os.environ['TASK'])`,
		},
		{
			name:     "the same selected through the job's defaults",
			accepted: false,
			job: `    defaults:
      run:
        shell: python
    env: {TASK: "${{ github.event.pull_request.body }}", CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - run: exec(os.environ['TASK'])`,
		},
		{
			name:     "bash, which is read",
			accepted: true,
			job: `    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
      - shell: bash
        run: go test ./...`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t,
				"on: pull_request_target\njobs:\n  probe:\n    runs-on: ubuntu-latest\n"+tc.job+"\n",
				tc.name)
			ok, missing := acceptedExecution(t, wf, "probe")
			require.Equal(t, tc.accepted, ok, "guard said %q", missing)
		})
	}
}

// Provenance travels: a party-chosen value written into $GITHUB_ENV under a
// fresh name is still that value when a later step executes the new name.
func TestProvenanceTravelsBetweenSteps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		steps    string
		accepted bool
	}{
		{
			name:     "renamed through the environment file, then executed",
			accepted: false,
			steps: `      - env: {TASK: "${{ github.event.pull_request.body }}"}
        run: echo "NEXT=$TASK" >> $GITHUB_ENV
      - run: eval "$NEXT"`,
		},
		{
			name:     "written in a form whose provenance cannot be read",
			accepted: false,
			steps: `      - env: {TASK: "${{ github.event.pull_request.body }}"}
        run: |
          { echo "NEXT<<EOF"; echo "$TASK"; echo EOF; } >> "$GITHUB_ENV"
      - run: eval "$NEXT"`,
		},
		{
			name:     "a value with no party-chosen provenance",
			accepted: true,
			steps: `      - run: echo "NEXT=go test ./..." >> $GITHUB_ENV
      - run: eval "$NEXT"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, `on: pull_request_target
jobs:
  probe:
    runs-on: ubuntu-latest
    env: {CREDENTIAL: "${{ secrets.BUNDLED_CONFIG }}"}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
        with: {ref: main}
`+tc.steps+"\n", tc.name)
			ok, missing := acceptedExecution(t, wf, "probe")
			require.Equal(t, tc.accepted, ok, "guard said %q", missing)
		})
	}
}
