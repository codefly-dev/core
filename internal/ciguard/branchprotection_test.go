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
	"Companion version",
	"Proto",
	"pnpm source evidence",
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
	Tags           []string `yaml:"tags"`
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

		out = append(out, safeChecksOf(t, path, wf)...)
	}
	return out
}

// safeChecksOf is checksSafeToRequire for one parsed workflow.
func safeChecksOf(t *testing.T, path string, wf workflow) []string {
	t.Helper()

	if !alwaysRunsOnPullRequestsToMain(t, path, wf) {
		return nil
	}

	// A job is skipped when something it needs fails. That is safe to
	// require only when the upstream is itself required: the failure has
	// already blocked the merge, so the skip adds no new way to be stuck.
	// Repeated until nothing new is admitted, because `needs` chains.
	safe := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for id, job := range wf.Jobs {
			// An `if:` can skip the job, and GitHub counts a skipped
			// required check as passing. The one `if:` admitted is an
			// aggregator's `always()`, which never skips it.
			if safe[id] || (job.If != "" && !aggregatesItsNeeds(job)) {
				continue
			}
			if aggregatesItsNeeds(job) && !aggregatorRunsAsWritten(wf) {
				continue
			}
			if _, ok := checkNames(id, job); !ok {
				continue
			}
			// An aggregator, like any other job, is admitted only once every
			// job it needs is: it stands for them, so each must always run
			// under a name known from the file. A matrix rewritten by
			// `exclude:` fails that and takes the aggregator with it, rather
			// than letting it stand for shards that never ran.
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

	// A job a safe aggregator needs is protected through it: requiring the
	// aggregator requires its success. Requiring it by name as well would
	// tie the ruleset to a matrix's size for no extra protection.
	covered := map[string]bool{}
	for id, job := range wf.Jobs {
		if safe[id] && aggregatesItsNeeds(job) && aggregatorRunsAsWritten(wf) {
			for _, need := range needsOf(job.Needs) {
				covered[need] = true
			}
		}
	}

	var out []string
	for id, job := range wf.Jobs {
		if !safe[id] || covered[id] {
			continue
		}
		names, _ := checkNames(id, job)
		out = append(out, names...)
	}
	return out
}

// aggregatorResults and aggregatorScript are the one aggregator shape this
// derivation recognises, held to the letter: a variation is not "probably
// equivalent", it is a job whose verdict nobody here has read.
const (
	aggregatorResults = "${{ join(needs.*.result, ' ') }}"
	aggregatorScript  = `echo "results: $RESULTS"
for result in $RESULTS; do
  if [ "$result" != success ]; then
    echo "a job Build needs did not succeed ($result); see the jobs above" >&2
    exit 1
  fi
done`
)

// aggregatesItsNeeds reports whether a job stands for the jobs it needs: it
// runs whatever happened to them (`if: always()`), so it always reports, and
// its one step fails unless every one of them succeeded. A failure, a skip and
// a cancellation upstream are all a red aggregator, never a skipped one -- so
// requiring the aggregator requires every job it needs.
//
// Without `always()` a failed need SKIPS the job, and a skipped required check
// passes. Without the step, `always()` alone would make it pass on any outcome.
//
// Keys are held to an allowlist, not checked against known hazards:
// `continue-on-error:` lets the refusal fail and the job pass, `shell:` or
// `working-directory:` decide what the script even is, and the next such key
// is one nobody here has thought of yet.
func aggregatesItsNeeds(job workflowJob) bool {
	if strings.TrimSpace(job.If) != "always()" && strings.TrimSpace(job.If) != "${{ always() }}" {
		return false
	}
	if len(needsOf(job.Needs)) == 0 || len(job.Steps) != 1 {
		return false
	}
	if !onlyKeys(job.keys, "name", "needs", "if", "runs-on", "permissions", "steps") {
		return false
	}
	// No credential: it reads `needs` and nothing else.
	if job.permissions.Kind != yaml.MappingNode || len(job.permissions.Content) != 0 {
		return false
	}
	step := job.Steps[0]
	if !onlyKeys(step.keys, "name", "env", "run") {
		return false
	}
	if len(step.Env) != 1 || step.Env["RESULTS"] != aggregatorResults {
		return false
	}
	return strings.TrimSpace(step.Run) == aggregatorScript
}

// aggregatorRunsAsWritten reports whether nothing workflow-wide can change how
// an aggregator's script runs: `defaults.run.shell` replaces bash, and `env:`
// reaches the shell (SHELLOPTS, BASH_ENV) before the script does.
func aggregatorRunsAsWritten(wf workflow) bool {
	return wf.Defaults.Kind == 0 && wf.Env.Kind == 0
}

func onlyKeys(keys []string, allowed ...string) bool {
	for _, key := range keys {
		if !contains(allowed, key) {
			return false
		}
	}
	return true
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

	// A RULESET payload, not the legacy branch-protection object. Protection on
	// this repository lives in `repos/.../rulesets`; the legacy
	// `branches/main/protection` endpoint returns 404, which the runbook used
	// to read as "`main` is unprotected" while ruleset 24397257 was active.
	var payload struct {
		Rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				RequiredStatusChecks []struct {
					Context string `json:"context"`
				} `json:"required_status_checks"`
			} `json:"parameters"`
		} `json:"rules"`
	}
	body := strings.Join(lines[start:end], "\n")
	require.NoError(t, json.Unmarshal([]byte(body), &payload),
		"%s: the apply payload is not valid JSON, so copy-pasting it fails", branchProtectionRunbook)

	var out []string
	found := false
	for _, rule := range payload.Rules {
		if rule.Type != "required_status_checks" {
			continue
		}
		found = true
		for _, check := range rule.Parameters.RequiredStatusChecks {
			out = append(out, check.Context)
		}
	}
	require.True(t, found,
		"%s: the apply payload carries no `required_status_checks` rule, so it "+
			"would replace the ruleset with one that requires nothing. A ruleset "+
			"PUT replaces the whole object.", branchProtectionRunbook)
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

// The aggregator is recognised only in the shape that refuses every non-success
// upstream. Each refused case below is a `Build` that could report green while
// a job it stands for did not succeed -- or report nothing at all.
func TestAggregatorIsRecognisedOnlyWhenItRefusesEveryOutcomeButSuccess(t *testing.T) {
	script := "        run: |\n          " + strings.ReplaceAll(aggregatorScript, "\n", "\n          ") + "\n"
	job := func(ifExpr, env, run string) string {
		return `on: pull_request
jobs:
  shard:
    name: Shard (${{ matrix.n }})
    strategy:
      matrix:
        n: [1, 2]
    steps:
      - run: go test
  build:
    name: Build
    needs: [shard]
    permissions: {}
` + ifExpr + `    steps:
      - name: every gate succeeded
` + env + run
	}
	env := "        env:\n          RESULTS: ${{ join(needs.*.result, ' ') }}\n"

	cases := []struct {
		name string
		yml  string
		want []string
	}{
		{"always() aggregator stands for its needs", job("    if: always()\n", env, script), []string{"Build"}},
		{"expression spelling", job("    if: ${{ always() }}\n", env, script), []string{"Build"}},
		// Skipped when a shard fails, and a skipped required check passes; the
		// shards' own names are then the only protection.
		{"no if: is a plain job", job("", env, script), []string{"Build", "Shard (1)", "Shard (2)"}},
		{"success() can be skipped", job("    if: success()\n", env, script), []string{"Shard (1)", "Shard (2)"}},
		{"reads one need, not all", job("    if: always()\n",
			"        env:\n          RESULTS: ${{ needs.shard.result }}\n", script), []string{"Shard (1)", "Shard (2)"}},
		{"never refuses", job("    if: always()\n", env, "        run: echo \"$RESULTS\"\n"), []string{"Shard (1)", "Shard (2)"}},
		// The refusal runs, fails, and the job passes anyway.
		{"continue-on-error step", job("    if: always()\n", env+"        continue-on-error: true\n", script), []string{"Shard (1)", "Shard (2)"}},
		{"continue-on-error job", job("    if: always()\n    continue-on-error: true\n", env, script), []string{"Shard (1)", "Shard (2)"}},
		// The script is never run by bash.
		{"shell replaced", job("    if: always()\n", env+"        shell: 'true {0}'\n", script), []string{"Shard (1)", "Shard (2)"}},
		{"holds a credential", strings.Replace(job("    if: always()\n", env, script),
			"    permissions: {}\n", "    permissions:\n      contents: write\n", 1), []string{"Shard (1)", "Shard (2)"}},
		{"workflow-wide default shell", "defaults:\n  run:\n    shell: 'true {0}'\n" + job("    if: always()\n", env, script), []string{"Shard (1)", "Shard (2)"}},
		{"workflow-wide env", "env:\n  SHELLOPTS: noexec\n" + job("    if: always()\n", env, script), []string{"Shard (1)", "Shard (2)"}},
		// GitHub runs shard 2 only; the aggregator must not stand for a shard
		// that never ran, so it is not admitted at all and the guard fails.
		{"matrix rewritten by exclude", strings.Replace(job("    if: always()\n", env, script),
			"        n: [1, 2]\n", "        n: [1, 2]\n        exclude:\n          - n: 1\n", 1), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wf workflow
			require.NoError(t, yaml.Unmarshal([]byte(tc.yml), &wf))
			require.ElementsMatch(t, tc.want, safeChecksOf(t, "probe.yml", wf))
		})
	}
}
