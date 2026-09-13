package ciguard

import (
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

type protectionWorkflow struct {
	On   map[string]yaml.Node     `yaml:"on"`
	Jobs map[string]protectionJob `yaml:"jobs"`
}

type protectionJob struct {
	Name     string    `yaml:"name"`
	If       string    `yaml:"if"`
	Needs    yaml.Node `yaml:"needs"`
	Strategy struct {
		Matrix map[string]yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
}

// pullRequestTrigger is the part of `on.pull_request` that decides whether a
// check reports on every pull request to `main`.
type pullRequestTrigger struct {
	Branches    []string `yaml:"branches"`
	Paths       []string `yaml:"paths"`
	PathsIgnore []string `yaml:"paths-ignore"`
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
// `main` triggers this workflow.
//
// A `paths:` filter is disqualifying, and not as a matter of taste: a workflow
// the filter excludes does not run, so GitHub never receives its check at all
// and holds the merge open waiting for a status nothing will ever report. That
// is the difference between a check that FAILS (recoverable — push a fix) and
// one that never arrives (unrecoverable without admin access).
func alwaysRunsOnPullRequestsToMain(t *testing.T, path string, wf protectionWorkflow) bool {
	t.Helper()

	node, ok := wf.On["pull_request"]
	if !ok {
		return false
	}
	// A bare `pull_request:` carries no filters, so it runs on everything.
	if node.Kind != yaml.MappingNode {
		return true
	}

	var trigger pullRequestTrigger
	require.NoError(t, node.Decode(&trigger), "%s: cannot read on.pull_request", filepath.Base(path))

	if len(trigger.Paths) > 0 || len(trigger.PathsIgnore) > 0 {
		return false
	}
	// Omitting `branches:` means every branch, which includes main.
	if len(trigger.Branches) == 0 {
		return true
	}
	for _, branch := range trigger.Branches {
		if branch == "main" {
			return true
		}
	}
	return false
}

// checkNames returns the check names a job produces, or false when they cannot
// be known from the file.
//
// GitHub names a check after the job's `name:`, falling back to the job id, and
// appends the matrix values for a matrix job. A name interpolated from a matrix
// declared as a literal list is therefore knowable — `[go, next]` yields
// exactly two names. A matrix computed at run time is not: companions-build
// takes `include: ${{ fromJSON(needs.plan.outputs.companions) }}`, so its check
// names carry the companion versions of the day (`build (proto, 0.0.14, …)`)
// and change under protection's feet on every version bump.
func checkNames(id string, job protectionJob) ([]string, bool) {
	name := job.Name
	if name == "" {
		name = id
	}

	names := []string{name}
	for key, node := range job.Strategy.Matrix {
		// `include`/`exclude` adjust a matrix rather than declaring an axis.
		if key == "include" || key == "exclude" {
			if node.Kind != yaml.SequenceNode {
				return nil, false
			}
			continue
		}
		if node.Kind != yaml.SequenceNode {
			return nil, false
		}
		var values []string
		if err := node.Decode(&values); err != nil {
			return nil, false
		}

		placeholder := "${{ matrix." + key + " }}"
		var expanded []string
		for _, candidate := range names {
			if !strings.Contains(candidate, placeholder) {
				expanded = append(expanded, candidate)
				continue
			}
			for _, value := range values {
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

		var wf protectionWorkflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		if !alwaysRunsOnPullRequestsToMain(t, path, wf) {
			continue
		}

		// A job is skipped when something it needs fails. That is safe to
		// require only when the upstream is itself required: the failure has
		// already blocked the merge, so the skip adds no new way to be stuck.
		// Resolved to a fixed point because `needs` chains.
		safe := map[string]bool{}
		for range wf.Jobs {
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

// The pinned set must keep matching what the workflows actually produce.
// Protection lives in repository settings, so drift here is invisible until a
// merge hangs or a red pull request lands.
func TestChecksSafeToRequireAreExactlyTheDocumentedSet(t *testing.T) {
	require.ElementsMatch(t, requiredChecks, checksSafeToRequire(t),
		"the checks that are safe to require on `main` no longer match the set "+
			"this repository documents.\n\n"+
			"A check LEAVING the set (a new `paths:` filter, an added job-level "+
			"`if:`, a matrix that is no longer a literal list) means protection "+
			"is currently requiring a name that may never report, which blocks "+
			"every merge. A check JOINING it means protection does not cover a "+
			"gate that always runs.\n\n"+
			"Update `requiredChecks` above, %s, AND the repository's branch "+
			"protection settings — the settings are not in this repository and "+
			"nothing else will notice.", branchProtectionRunbook)
}

// The runbook is what an operator reads while editing settings, so a name it
// gets wrong is applied by hand and stays wrong.
func TestBranchProtectionRunbookNamesEveryRequiredCheck(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), branchProtectionRunbook))
	require.NoError(t, err)
	runbook := string(raw)

	for _, name := range requiredChecks {
		require.Contains(t, runbook, name,
			"%s does not name the required check %q, so whoever applies "+
				"protection from it leaves that check out", branchProtectionRunbook, name)
	}
}
