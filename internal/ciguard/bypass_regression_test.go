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
// Each of these was accepted at some head of this pull request, and each was
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
			name:     "an attacker-chosen upstream conclusion, sampled as success",
			triggers: "workflow_run:\n    workflows: [go]\n    types: [completed]\n",
			gate:     "github.event.workflow_run.conclusion == 'failure'",
			why:      "the scenarios pinned conclusion to success, so this read as definitely false; an attacker need only make their own upstream run fail",
		},
		{
			name:     "a negated conjunction",
			triggers: "pull_request:\n",
			gate:     "!(always() && github.event_name == 'push' && github.ref == 'refs/heads/main')",
			why:      "a reader that split on && accepted this as requiring push of main, although it is TRUE on a pull request",
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

// A trigger with no scenario must come back unmodelled rather than silently
// contributing nothing: "added by GitHub next year" has to mean a failing
// test. `workflow_run` was exempt here once because another guard was said to
// cover it, and that guard only looked at write-capable jobs.
func TestATriggerWithNoScenarioIsReportedRatherThanSkipped(t *testing.T) {
	// Read through a whole workflow, the way the guards do: `on:` is a node
	// inside a document, and a node decoded on its own is a different shape.
	withTrigger := func(trigger string) yaml.Node {
		wf := parseIsolatedDocument(t, "name: probe\non:\n  "+trigger+"\njobs:\n  probe:\n    runs-on: ubuntu-latest\n", trigger)
		return wf.On
	}

	hostile, unmodelled := hostileScenariosFor(withTrigger("discussion:\n    types: [created]"))
	require.Empty(t, hostile)
	require.Equal(t, []string{"discussion"}, unmodelled,
		"a trigger with no scenario must be reported: a silent pass is how workflow_run came to be checked by no guard at all")

	hostile, unmodelled = hostileScenariosFor(withTrigger("workflow_run:\n    workflows: [go]"))
	require.Empty(t, unmodelled)
	require.Len(t, hostile, 2, "workflow_run has two hostile shapes and is exempt from neither")
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
