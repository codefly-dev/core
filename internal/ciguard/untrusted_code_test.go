package ciguard

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A continuous-integration job holds one credential whether or not it asks for
// one: GITHUB_TOKEN is injected into every job, and `permissions:` decides what
// it can do. The question these guards answer is therefore not "does this job
// use a token" but "could the code this job runs have been written by whoever
// opened the pull request, while that token can write".
//
// Three shapes put a job on the wrong side of that line, and none of them is
// visible in a diff that only adds a step:
//
//   - a `pull_request` workflow whose `permissions:` grant a write, which hands
//     the write token to every step in it, the one running the suite included;
//   - a `workflow_run` job, which runs with the base repository's permissions
//     however the run that triggered it was produced, so without an event and
//     head-repository guard it is reachable from a pull request's run;
//   - a reusable (`workflow_call`) workflow that declares no `permissions:` at
//     all, which runs the CALLER's code at whatever the caller granted — a
//     repository this one cannot see, let alone review.
//
// `permissions:` is also the only lever available: GitHub scopes it per JOB,
// never per step, so "scoped to the steps that need it" is spelled as a
// separate job that runs no code under review. That is why the coverage badge
// and the release tag are jobs of their own rather than a step in the job that
// has just run `go test`.

// writeAll is the scope name reported for `permissions: write-all`, which
// grants every scope at once and names none of them.
const writeAll = "(write-all)"

// permissionGrant is what one `permissions:` block grants.
type permissionGrant struct {
	// declared is false when no block is present. An absent block is not
	// "nothing granted": the job inherits the repository default (which may be
	// read-write) or, in a reusable workflow, the caller's grant. Either way
	// what the token can do is not readable here, which is itself the defect.
	declared bool
	writes   []string
}

// readPermissions reads a `permissions:` node in every form GitHub accepts:
// the `read-all`/`write-all` scalars, a mapping of scope to access, and the
// empty mapping that grants nothing.
func readPermissions(node yaml.Node) permissionGrant {
	switch node.Kind {
	case yaml.ScalarNode:
		switch strings.TrimSpace(node.Value) {
		case "write-all":
			return permissionGrant{declared: true, writes: []string{writeAll}}
		case "read-all", "":
			return permissionGrant{declared: true}
		}
		return permissionGrant{declared: true}
	case yaml.MappingNode:
		grant := permissionGrant{declared: true}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if strings.TrimSpace(node.Content[i+1].Value) == "write" {
				grant.writes = append(grant.writes, node.Content[i].Value)
			}
		}
		sort.Strings(grant.writes)
		return grant
	}
	return permissionGrant{}
}

// effectivePermissions resolves what a job's token can do: its own block when
// it has one, otherwise the workflow's.
func effectivePermissions(wf permissionedWorkflow, job permissionedJob) permissionGrant {
	if grant := readPermissions(job.Permissions); grant.declared {
		return grant
	}
	return readPermissions(wf.Permissions)
}

// writeCapable reports whether a job's token can write, treating an undeclared
// grant as write-capable: a guard that reads silence as "no permissions" is a
// guard that passes every workflow that forgot to declare any.
func writeCapable(grant permissionGrant) bool {
	return !grant.declared || len(grant.writes) > 0
}

func (g permissionGrant) String() string {
	if !g.declared {
		return "no permissions: block (inherits the repository or caller default)"
	}
	if len(g.writes) == 0 {
		return "read-only"
	}
	return "write on " + strings.Join(g.writes, ", ")
}

// permissionedStep carries the `with:` of a step so a checkout's `ref:` can be
// read. It is separate from workflowStep because that type is shaped for the
// secret and action-pin guards.
type permissionedStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

// describe names a step in a failure. Most checkout steps carry no `name:`, and
// "step \"\"" tells a reader nothing about which one to open.
func (s permissionedStep) describe() string {
	if s.Name != "" {
		return fmt.Sprintf("step %q", s.Name)
	}
	if s.Uses != "" {
		return fmt.Sprintf("step `uses: %s`", s.Uses)
	}
	return "an unnamed step"
}

type permissionedJob struct {
	If          string             `yaml:"if"`
	Permissions yaml.Node          `yaml:"permissions"`
	Steps       []permissionedStep `yaml:"steps"`
}

type permissionedWorkflow struct {
	On          yaml.Node                  `yaml:"on"`
	Permissions yaml.Node                  `yaml:"permissions"`
	Jobs        map[string]permissionedJob `yaml:"jobs"`
}

// loadPermissionedWorkflows reads every workflow, keyed by path, in stable
// order.
func loadPermissionedWorkflows(t *testing.T) ([]string, map[string]permissionedWorkflow) {
	t.Helper()

	paths := workflowFiles(t)
	out := make(map[string]permissionedWorkflow, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf permissionedWorkflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)
		out[path] = wf
	}
	return paths, out
}

// jobIDs returns a workflow's job identifiers in stable order, so a failure
// names the same job first on every run.
func jobIDs(wf permissionedWorkflow) []string {
	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// cannotRunOnAPullRequest reports whether gate is PROVABLY false when the event
// is a pull request.
//
// This asked whether the condition contained two clauses, which `... || true`
// and `!(... && ...)` both satisfy while running on a pull request. It is now
// decided by evaluating the expression -- see expression_test.go -- and an
// unknown result is not an answer: a condition that turns on something no
// scenario fixes has established nothing.
func cannotRunOnAPullRequest(t *testing.T, gate string) bool {
	t.Helper()

	reached, err := canRunUnder(gate, pullRequest)
	require.NoError(t, err,
		"a condition this package cannot parse is one it cannot judge: %q", gate)
	return reached == triFalse
}

// A `pull_request` run checks out the pull request's head: for the whole job,
// every step after the checkout is running code the author of that branch
// wrote. `permissions:` is job-wide, so a workflow-level write grant reaches
// the step that runs the suite as surely as the step that needed it.
//
// A fork's run is not the only case. GitHub downgrades the token to read-only
// and withholds secrets for a fork, but a branch pushed to this repository is
// unmerged code that has not been reviewed yet and keeps the full grant.
//
// The fix for such a workflow is never a narrower token on the step — there is
// no such thing — but the write moved into a job of its own, gated to a push of
// the default branch, which is what this allows for.
func TestNoJobRunsPullRequestCodeWithAWriteToken(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		if !runsOnPullRequest(workflow{On: wf.On}) {
			continue
		}
		checked++

		for _, id := range jobIDs(wf) {
			job := wf.Jobs[id]
			grant := effectivePermissions(wf, job)
			if !writeCapable(grant) {
				continue
			}
			require.True(t, cannotRunOnAPullRequest(t, job.If),
				"%s: job %q can be reached by a pull request and its token has %s "+
					"(condition: %q). A pull request's run checks out that pull "+
					"request's code, and `permissions:` cannot be narrowed per step, so "+
					"every step in the job holds that grant. Declare the job read-only "+
					"and move the write into a job whose condition is provably false on "+
					"a pull request -- `github.event_name == 'push' && github.ref == "+
					"'refs/heads/main'` -- which runs no code under review.",
				filepath.Base(path), id, grant, job.If)
		}
	}
	require.NotZero(t, checked, "no workflow runs on pull requests, so this guard proves nothing")
}

// A `workflow_run` job always runs with the BASE repository's permissions and
// from the default branch's copy of the workflow file, whatever produced the
// run it reacts to. The `branches:` filter does not establish that: it matches
// the triggering run's HEAD branch, a name the head repository chooses.
//
// Two hostile shapes, asserted separately, because each is stopped by a
// DIFFERENT condition: a run produced by a pull request (which only the event
// condition refuses) and a run produced by a push to a fork (which only the
// head-repository condition refuses). A job carrying one of the two passes
// against one shape and fails against the other, which is the point -- and it
// is why this asks about reachability rather than about which clauses are
// present.
func TestNoWorkflowRunJobHoldsAWriteTokenReachableFromAnUntrustedRun(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		if !contains(triggers(wf.On), "workflow_run") {
			continue
		}
		checked++

		for _, id := range jobIDs(wf) {
			job := wf.Jobs[id]
			grant := effectivePermissions(wf, job)
			if !writeCapable(grant) {
				continue
			}
			for _, hostile := range []scenario{workflowRunFromAPullRequest, workflowRunFromAForkPush} {
				reached, err := canRunUnder(job.If, hostile)
				require.NoError(t, err,
					"%s: job %q has a condition this package cannot judge: %q",
					filepath.Base(path), id, job.If)
				require.Equal(t, triFalse.String(), reached.String(),
					"%s: job %q runs on workflow_run with %s, and its condition (%q) "+
						"can still be reached by %s. The `branches:` filter matches the "+
						"triggering run's head branch, which the head repository names, "+
						"so the condition has to refuse both the event and the head "+
						"repository -- and refuse them in a way that evaluating the "+
						"expression confirms.",
					filepath.Base(path), id, grant, job.If, hostile.name)
			}
		}
	}
	require.NotZero(t, checked, "no workflow runs on workflow_run, so this guard proves nothing")
}

// triggeringCommitRefs are the `workflow_run` payload fields that name the
// triggering run's code. Passing one to a checkout makes that code the tree
// every later step runs from.
var triggeringCommitRefs = []string{
	"workflow_run.head_sha",
	"workflow_run.head_branch",
	"workflow_run.head_commit",
}

// The guards above decide WHETHER a job runs. This decides what it runs when it
// does, and it is the half that does not depend on an expression being right.
//
// A `workflow_run` job that checks out `head_sha` runs whatever that commit
// contains. A job that checks out the default branch instead runs only reviewed
// code, and treats the triggering sha as data — something to validate against
// the default branch's history before using it. Then a guard that is wrong
// costs a refusal rather than an execution.
func TestNoWorkflowRunJobChecksOutTheTriggeringCommit(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		if !contains(triggers(wf.On), "workflow_run") {
			continue
		}
		checked++

		for _, id := range jobIDs(wf) {
			for _, step := range wf.Jobs[id].Steps {
				ref, ok := step.With["ref"].(string)
				if !ok {
					continue
				}
				for _, field := range triggeringCommitRefs {
					require.NotContains(t, ref, field,
						"%s: job %q %s checks out %s. A workflow_run job runs with "+
							"this repository's permissions; checking out the triggering "+
							"run's code makes that code the tree every later step runs "+
							"from. Check out the default branch and validate the "+
							"triggering sha against its history instead.",
						filepath.Base(path), id, step.describe(), field)
				}
			}
		}
	}
	require.NotZero(t, checked, "no workflow runs on workflow_run, so this guard proves nothing")
}

// A reusable workflow runs the CALLING repository's code, at a ref the caller
// chooses — including a pull request's head. It cannot see how it is called,
// and the caller cannot see what it needs, so a missing `permissions:` here
// means neither side knows what the token can do: the job simply takes whatever
// the caller granted.
//
// Declaring it in the reusable workflow settles it from this side for every
// caller at once. The declaration can only narrow the caller's grant, so a job
// that runs the caller's build or suite declares read.
func TestReusableWorkflowJobsDeclareAReadOnlyToken(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		if !contains(triggers(wf.On), "workflow_call") {
			continue
		}
		checked++

		for _, id := range jobIDs(wf) {
			job := wf.Jobs[id]
			grant := readPermissions(job.Permissions)
			require.True(t, grant.declared,
				"%s: job %q declares no `permissions:`, so it runs the calling "+
					"repository's code at whatever that repository granted — a grant "+
					"this workflow cannot read and that caller cannot size. Declare "+
					"`permissions:` on the job.",
				filepath.Base(path), id)
			require.Empty(t, grant.writes,
				"%s: job %q runs the calling repository's code with %s. A caller may "+
					"dispatch this workflow from a pull request, so the code it checks "+
					"out is unreviewed; the credential a release needs belongs to the "+
					"caller's own job, passed as a secret, not to this token.",
				filepath.Base(path), id, grant)
		}
	}
	require.NotZero(t, checked, "no reusable workflow found, so this guard proves nothing")
}

// readPermissions decides every assertion above, so a form it reads as "no
// writes" is an escape hatch that makes all of them pass. These are the forms
// GitHub accepts, including the two scalars that name no scope at all.
func TestReadPermissionsUnderstandsEveryFormGitHubAccepts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		yaml     string
		declared bool
		writes   []string
	}{
		{name: "absent", yaml: "jobs: {}", declared: false},
		{name: "write-all", yaml: "permissions: write-all", declared: true, writes: []string{writeAll}},
		{name: "read-all", yaml: "permissions: read-all", declared: true},
		{name: "empty mapping", yaml: "permissions: {}", declared: true},
		{
			name:     "mapping with a write",
			yaml:     "permissions:\n  contents: read\n  packages: write\n",
			declared: true,
			writes:   []string{"packages"},
		},
		{
			name:     "mapping with several writes, reported in stable order",
			yaml:     "permissions:\n  pull-requests: write\n  contents: write\n",
			declared: true,
			writes:   []string{"contents", "pull-requests"},
		},
		{name: "all read", yaml: "permissions:\n  contents: read\n", declared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wf permissionedWorkflow
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &wf))

			grant := readPermissions(wf.Permissions)
			require.Equal(t, tc.declared, grant.declared)
			require.Equal(t, tc.writes, grant.writes)
			require.Equal(t, len(tc.writes) > 0 || !tc.declared, writeCapable(grant),
				"a %s grant reads as %q", tc.name, grant)
		})
	}
}

// effectivePermissions is the other half of every assertion: a job block wins,
// and a job with no block of its own takes the workflow's.
func TestEffectivePermissionsPrefersTheJobBlock(t *testing.T) {
	const doc = `
permissions:
  contents: write
jobs:
  inherits:
    steps: []
  narrows:
    permissions:
      contents: read
    steps: []
`
	var wf permissionedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(doc), &wf))

	require.Equal(t, []string{"contents"}, effectivePermissions(wf, wf.Jobs["inherits"]).writes)
	require.Empty(t, effectivePermissions(wf, wf.Jobs["narrows"]).writes)
	require.False(t, writeCapable(effectivePermissions(wf, wf.Jobs["narrows"])))
}

// The guards above are worth exactly what their job discovery is worth: a
// workflow whose jobs this package cannot read is a workflow none of them
// constrain. Every file must parse into at least one job.
func TestEveryWorkflowYieldsJobsToTheseGuards(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)
	for _, path := range paths {
		require.NotEmpty(t, workflows[path].Jobs,
			"%s parsed into no jobs, so none of the untrusted-code guards in this "+
				"file examine it", filepath.Base(path))
		require.NotEmpty(t, triggers(workflows[path].On),
			"%s parsed into no triggers, so the guards in this file cannot tell "+
				"whether it can run untrusted code", filepath.Base(path))
	}
}

// The guards above ask whether a write-capable job can run untrusted code. This
// asks where the write is written down, because that decides which jobs are
// write-capable NEXT.
//
// A workflow-scope grant is inherited by every job in the file, including the
// one somebody adds a year from now. `permissions:` cannot be narrowed per
// step, so declaring the write on the single job that performs it is the only
// scoping GitHub offers — and it makes the next job's grant a decision rather
// than a leftover.
func TestNoWorkflowGrantsAWriteAtWorkflowScope(t *testing.T) {
	paths, workflows := loadPermissionedWorkflows(t)

	for _, path := range paths {
		grant := readPermissions(workflows[path].Permissions)
		if !grant.declared {
			continue
		}
		require.Empty(t, grant.writes,
			"%s grants %s at WORKFLOW scope, so every job in it inherits that "+
				"write — the jobs there now and any added later. Declare "+
				"`permissions: contents: read` at workflow scope and put the write "+
				"on the one job that performs it.",
			filepath.Base(path), grant)
	}
}
