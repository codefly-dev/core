package ciguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Branch protection names the status checks it requires as STRINGS, in
// repository settings, where nothing in this repository can test them. Picking
// that set wrong is worse than leaving the branch open: a required check that
// never reports holds every merge at "Expected — waiting for status to be
// reported" until someone with admin access notices.
//
// So the set is derived here from the workflows themselves and pinned, and the
// runbook that applies it is held to the same names. A workflow edit that would
// break protection — or quietly shrink what it covers — fails in CI rather than
// in the settings page.
//
// The set matters because nothing enforced it: #447 merged with `Build:
// FAILURE` visible on the pull request.
//
// Every rule below FAILS CLOSED. Deriving too few checks under-protects `main`,
// which is no worse than today and is fixed by editing a file; deriving a name
// that does not exist wedges every merge and needs admin access to clear. When
// a name cannot be known exactly from the file, this refuses to guess.

// requiredChecks is the set that is safe to require on `main`, and exactly what
// docs/runbooks/branch-protection.md tells an operator to apply.
var requiredChecks = []string{
	"Build",
	"Registry cache clean runner (go)",
	"Registry cache clean runner (next)",
	"Registry cache cold (go)",
	"Registry cache cold (next)",
}

const branchProtectionRunbook = "docs/runbooks/branch-protection.md"

// pullRequestTrigger is the part of `on.pull_request` that decides whether a
// check reports on every pull request to `main`.
type pullRequestTrigger struct {
	Branches       []string `yaml:"branches"`
	BranchesIgnore []string `yaml:"branches-ignore"`
	Paths          []string `yaml:"paths"`
	PathsIgnore    []string `yaml:"paths-ignore"`
	Types          []string `yaml:"types"`
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// needsOf normalises `needs:`, which GitHub accepts as a bare string or a list.
func needsOf(node yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		return []string{node.Value}
	case yaml.SequenceNode:
		var out []string
		if err := node.Decode(&out); err != nil {
			return nil
		}
		return out
	}
	return nil
}

// alwaysRunsOnPullRequestsToMain reports whether every pull request targeting
// `main`, and every push to one, triggers this workflow.
//
// Each filter it rejects produces a different broken merge:
//
//   - `paths:`/`paths-ignore:` — an excluded pull request does not run the
//     workflow at all, so GitHub never receives the check and holds the merge
//     waiting for a status nothing will report. That is the difference between a
//     check that FAILS (recoverable — push a fix) and one that never arrives
//     (unrecoverable without admin access).
//   - `branches-ignore: [main]` — the workflow runs on no pull request to main,
//     so the same wedge applies.
//   - `types:` without `opened` — no check on a new pull request; the wedge
//     again. Without `synchronize` the check reports once and never again, so
//     protection is satisfied by a result from an earlier commit: a red pull
//     request merges, which is #447's failure re-entering through its own fix.
func alwaysRunsOnPullRequestsToMain(t *testing.T, path string, wf workflow) bool {
	t.Helper()

	if !contains(triggers(wf.On), "pull_request") {
		return false
	}

	node, ok := triggerNode(wf.On, "pull_request")
	// `on: pull_request` or `on: [pull_request]` carries no filters at all.
	if !ok || node.Kind != yaml.MappingNode {
		return true
	}

	var trigger pullRequestTrigger
	require.NoError(t, node.Decode(&trigger), "%s: cannot read on.pull_request", filepath.Base(path))

	if len(trigger.Paths) > 0 || len(trigger.PathsIgnore) > 0 {
		return false
	}
	// An explicit `types:` replaces the default set, so it must still cover
	// both "a pull request appeared" and "its head moved".
	if len(trigger.Types) > 0 {
		for _, needed := range []string{"opened", "synchronize"} {
			if !contains(trigger.Types, needed) {
				return false
			}
		}
	}
	if contains(trigger.BranchesIgnore, "main") {
		return false
	}
	// Omitting `branches:` means every branch, which includes main.
	if len(trigger.Branches) > 0 && !contains(trigger.Branches, "main") {
		return false
	}
	return true
}

// matrixAxis is one matrix dimension whose values are written out literally.
type matrixAxis struct {
	key    string
	values []string
}

// matrixAxes reads a job's matrix in declaration order — the order GitHub joins
// values in a check name. `adjusted` reports an `include:`/`exclude:` key, which
// rewrites the combination set rather than declaring a dimension.
func matrixAxes(node yaml.Node) (axes []matrixAxis, adjusted bool, ok bool) {
	if node.Kind == 0 {
		return nil, false, true
	}
	if node.Kind != yaml.MappingNode {
		return nil, false, false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if key == "include" || key == "exclude" {
			adjusted = true
			continue
		}
		if value.Kind != yaml.SequenceNode {
			return nil, false, false
		}
		var values []string
		if err := value.Decode(&values); err != nil {
			return nil, false, false
		}
		axes = append(axes, matrixAxis{key: key, values: values})
	}
	return axes, adjusted, true
}

// checkNames returns the check names a job produces, or false when they cannot
// be known exactly from the file.
//
// GitHub names a check after the job's `name:`, falling back to the job id, and
// for a matrix job it appends the matrix values — `build (proto, 0.0.14,
// companions/proto/Dockerfile, ., linux/amd64,linux/arm64, codefly)` is how
// companions-build's unnamed `build` job appears. Three shapes are therefore
// refused rather than guessed at:
//
//   - No `name:` on a matrix job. The real names carry the values, so the job
//     id alone is a context that never reports. Giving the job an explicit
//     `name:` that interpolates the matrix makes it requirable again.
//   - `include:`/`exclude:`. The combination set is rewritten, and in
//     companions-build it is `${{ fromJSON(needs.plan.outputs.companions) }}`,
//     computed at run time from another job's output.
//   - A `name:` that does not interpolate every axis. Those combinations all
//     report under one identical name, and which of them satisfies protection
//     is not defined.
func checkNames(id string, job workflowJob) ([]string, bool) {
	axes, adjusted, ok := matrixAxes(job.Strategy.Matrix)
	if !ok || adjusted {
		return nil, false
	}

	if job.Name == "" {
		if len(axes) > 0 {
			return nil, false
		}
		return []string{id}, true
	}

	names := []string{job.Name}
	for _, axis := range axes {
		placeholder := "${{ matrix." + axis.key + " }}"
		if !strings.Contains(job.Name, placeholder) {
			return nil, false
		}
		var expanded []string
		for _, candidate := range names {
			for _, value := range axis.values {
				expanded = append(expanded, strings.ReplaceAll(candidate, placeholder, value))
			}
		}
		names = expanded
	}

	for _, name := range names {
		if strings.Contains(name, "${{") {
			return nil, false
		}
	}
	return names, true
}

// checksSafeToRequire derives, from the workflows, every check that can be
// required on `main` without ever deadlocking a merge.
func checksSafeToRequire(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf workflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		if !alwaysRunsOnPullRequestsToMain(t, path, wf) {
			continue
		}

		// A job is skipped when something it needs fails. That is safe to
		// require only when the upstream is itself required: the failure has
		// already blocked the merge, so the skip adds no new way to be stuck.
		// Repeated until nothing new is admitted, because `needs` chains.
		safe := map[string]bool{}
		for changed := true; changed; {
			changed = false
			for id, job := range wf.Jobs {
				if safe[id] || job.If != "" {
					continue
				}
				if _, ok := checkNames(id, job); !ok {
					continue
				}
				blocked := false
				for _, need := range needsOf(job.Needs) {
					if !safe[need] {
						blocked = true
					}
				}
				if !blocked {
					safe[id] = true
					changed = true
				}
			}
		}

		for id, job := range wf.Jobs {
			if !safe[id] {
				continue
			}
			names, _ := checkNames(id, job)
			out = append(out, names...)
		}
	}
	return out
}

// requiredContextsInRunbook returns the contexts in the runbook's apply
// payload — the JSON an operator actually PUTs, not prose mentioning a name.
func requiredContextsInRunbook(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), branchProtectionRunbook))
	require.NoError(t, err)

	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "<<'JSON'") {
			start = i + 1
			break
		}
	}
	require.NotEqual(t, -1, start,
		"%s no longer contains a `<<'JSON'` heredoc, so the payload an operator "+
			"applies cannot be checked against the derived set", branchProtectionRunbook)

	end := -1
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "JSON" {
			end = i
			break
		}
	}
	require.NotEqual(t, -1, end, "%s: unterminated JSON heredoc", branchProtectionRunbook)

	var payload struct {
		RequiredStatusChecks struct {
			Checks []struct {
				Context string `json:"context"`
			} `json:"checks"`
		} `json:"required_status_checks"`
	}
	body := strings.Join(lines[start:end], "\n")
	require.NoError(t, json.Unmarshal([]byte(body), &payload),
		"%s: the apply payload is not valid JSON, so copy-pasting it fails", branchProtectionRunbook)

	out := make([]string, 0, len(payload.RequiredStatusChecks.Checks))
	for _, check := range payload.RequiredStatusChecks.Checks {
		out = append(out, check.Context)
	}
	return out
}

// The pinned set must keep matching what the workflows actually produce.
// Protection lives in repository settings, so drift here is invisible until a
// merge hangs or a red pull request lands.
func TestChecksSafeToRequireAreExactlyTheDocumentedSet(t *testing.T) {
	require.ElementsMatch(t, requiredChecks, checksSafeToRequire(t),
		"the checks that are safe to require on `main` no longer match the set "+
			"this repository documents.\n\n"+
			"A check LEAVING the set (a new `paths:` filter, an added job-level "+
			"`if:`, a `types:` that drops opened/synchronize, a matrix that is no "+
			"longer a literal list) means protection is currently requiring a name "+
			"that may never report, which blocks every merge.\n\n"+
			"A check JOINING it means you added a gate that always runs: protection "+
			"does not cover it yet. If you added a matrix job, give it an explicit "+
			"`name:` that interpolates every matrix axis, or its real check name "+
			"carries the matrix values and cannot be required.\n\n"+
			"Update `requiredChecks` above, the apply payload in %s, AND the "+
			"repository's branch protection settings — the settings are not in this "+
			"repository and nothing else will notice.", branchProtectionRunbook)
}

// The runbook's payload is what an operator PUTs, so it must carry exactly the
// derived contexts: one missing leaves a gate unprotected (dropping `Build`
// re-opens the #447 hole), and one extra can be a context that never reports,
// which wedges every merge.
func TestBranchProtectionRunbookPayloadRequiresExactlyTheDerivedChecks(t *testing.T) {
	require.ElementsMatch(t, requiredChecks, requiredContextsInRunbook(t),
		"the `gh api` payload in %s does not require exactly the derived set. "+
			"Prose naming a check is not enough — the payload is what gets applied.",
		branchProtectionRunbook)
}

// checkNames must refuse every shape whose real check name it cannot know.
// Each case here is a context that would never report, so requiring it would
// hold every merge open.
func TestCheckNamesRefusesNamesItCannotKnow(t *testing.T) {
	cases := []struct {
		name  string
		yml   string
		want  []string
		wantK bool
	}{
		{
			name: "matrix job with no explicit name is refused",
			// Real names are `lint (1.27)` and `lint (1.28)`, never `lint`.
			yml: `jobs:
  lint:
    strategy:
      matrix:
        go: ["1.27", "1.28"]
`,
			wantK: false,
		},
		{
			name: "literal include with a static name is refused",
			yml: `jobs:
  verify:
    name: Verify
    strategy:
      matrix:
        include:
          - language: go
          - language: next
`,
			wantK: false,
		},
		{
			name: "name that ignores an axis is refused",
			yml: `jobs:
  verify:
    name: Verify
    strategy:
      matrix:
        language: [go, next]
`,
			wantK: false,
		},
		{
			name: "run-time include is refused",
			yml: `jobs:
  build:
    strategy:
      matrix:
        include: ${{ fromJSON(needs.plan.outputs.companions) }}
`,
			wantK: false,
		},
		{
			name: "plain job falls back to its id",
			yml: `jobs:
  plan:
    steps:
      - run: echo
`,
			want:  []string{"plan"},
			wantK: true,
		},
		{
			name: "named job with no matrix",
			yml: `jobs:
  build:
    name: Build
    steps:
      - run: echo
`,
			want:  []string{"Build"},
			wantK: true,
		},
		{
			name: "name interpolating its only axis expands",
			yml: `jobs:
  cache-cold:
    name: Registry cache cold (${{ matrix.language }})
    strategy:
      matrix:
        language: [go, next]
`,
			want:  []string{"Registry cache cold (go)", "Registry cache cold (next)"},
			wantK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wf workflow
			require.NoError(t, yaml.Unmarshal([]byte(tc.yml), &wf))
			require.Len(t, wf.Jobs, 1)
			for id, job := range wf.Jobs {
				names, ok := checkNames(id, job)
				require.Equal(t, tc.wantK, ok)
				if tc.wantK {
					require.Equal(t, tc.want, names)
				}
			}
		})
	}
}

// A trigger filter that stops a check reporting on some pull request to `main`
// must disqualify it, or protection waits forever on a status that never comes.
func TestPullRequestTriggersThatCannotGateAMergeAreRejected(t *testing.T) {
	cases := []struct {
		name string
		yml  string
		want bool
	}{
		{"bare pull_request", "on: pull_request\njobs: {}\n", true},
		{"list form", "on: [pull_request, push]\njobs: {}\n", true},
		{"branches main", "on:\n  pull_request:\n    branches: [main]\njobs: {}\n", true},
		{"branches without main", "on:\n  pull_request:\n    branches: [release]\njobs: {}\n", false},
		{"branches-ignore main", "on:\n  pull_request:\n    branches-ignore: [main]\njobs: {}\n", false},
		{"branches-ignore other", "on:\n  pull_request:\n    branches-ignore: [docs]\njobs: {}\n", true},
		{"paths filter", "on:\n  pull_request:\n    paths: [companions/**]\njobs: {}\n", false},
		{"paths-ignore filter", "on:\n  pull_request:\n    paths-ignore: [docs/**]\njobs: {}\n", false},
		{"types without opened", "on:\n  pull_request:\n    types: [synchronize]\njobs: {}\n", false},
		{"types without synchronize", "on:\n  pull_request:\n    types: [opened]\njobs: {}\n", false},
		{"types covering both", "on:\n  pull_request:\n    types: [opened, synchronize, reopened]\njobs: {}\n", true},
		{"no pull_request trigger", "on:\n  push:\n    branches: [main]\njobs: {}\n", false},
		{"workflow_call only", "on:\n  workflow_call:\n    inputs:\n      x:\n        type: string\njobs: {}\n", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wf workflow
			require.NoError(t, yaml.Unmarshal([]byte(tc.yml), &wf))
			require.Equal(t, tc.want, alwaysRunsOnPullRequestsToMain(t, "probe.yml", wf))
		})
	}
}
