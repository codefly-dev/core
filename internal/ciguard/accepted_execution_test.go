package ciguard

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Trigger identity cannot establish which code a job executes, so no trigger
// exempts a job here. A job holding a credential is accepted only if
// one of three things is PROVED about it:
//
//  1. its condition is false under every hostile scenario its workflow's
//     triggers admit -- it cannot run at all (checked by the callers of this
//     file, via hostileScenariosFor);
//  2. it pins what it runs: every checkout names the default branch
//     explicitly, no execution surface is chosen by the triggering party, and
//     it hands no secret to a workflow this package cannot read;
//  3. it proves what it runs: an ancestry refusal precedes every step that
//     executes repository code, so an unmerged commit is refused before any of
//     it runs.
//
// Trigger-shaped reasoning is gone from all three. A schedule really does run
// the default branch's workflow and a dispatcher really does need write
// access; neither fact says anything about the ref the job then checks out,
// which is what the constructions used.

// credentialJobIsAccepted is THE decision, and both guards call it.
//
// A job holding a credential is accepted only when what it EXECUTES is
// established: every checkout names the default branch with no party-chosen
// execution input, or a refusal proves the commit was merged before anything
// runs. That obligation does not lift, and in particular it is not lifted by
// the job being unreachable from every hostile trigger -- those are different
// questions. Unreachability says a hostile trigger cannot START the job; it
// says nothing about what the job runs when it starts legitimately, and a job
// gated to a push of the default branch can still check out another branch and
// run it with the credential.
//
// Unreachability is reported, because it is useful when reading a failure, and
// it decides nothing.
func credentialJobIsAccepted(t *testing.T, wf isolatedWorkflow, id string, hostile []scenario) (bool, string) {
	t.Helper()

	established, missing := acceptedExecution(t, wf, id)
	if established {
		// Execution is established. The remaining question is the provenance
		// of the PROGRAM -- the workflow file itself, which GitHub takes from
		// the triggering ref. A pinned checkout says nothing about it: an
		// unrestricted `push` runs the file from the branch that was pushed,
		// so an inline `run:` in it is the pusher's program however carefully
		// the checkout names the default branch.
		if trusted, untrusted := workflowSourceIsTrusted(t, wf.On); !trusted {
			if unreachable, _ := provablyUnreachable(t, wf.Jobs[id].If, hostile); !unreachable {
				return false, "this workflow's own file comes from a ref the " +
					"triggering party influences (" + strings.Join(untrusted, ", ") +
					"), so its inline program has no established provenance, and the " +
					"job is not provably unreachable from those situations either"
			}
		}
		return true, ""
	}

	note := "it does not establish what it executes: " + missing
	if unreachable, why := provablyUnreachable(t, wf.Jobs[id].If, hostile); unreachable {
		return false, note +
			" (it IS unreachable from every hostile situation, which does not " +
			"establish what it runs when triggered legitimately)"
	} else if why != "" {
		return false, note + " (and it is not provably unreachable either: " + why + ")"
	}
	return false, note
}

// partyChosenContexts are the context roots whose values the triggering party
// supplies. An execution surface built from one of these runs code this
// repository has not reviewed.
var partyChosenContexts = []string{"inputs", "github.event", "matrix", "needs"}

// acceptedExecution reports whether a job proves what it runs, by pinning or by
// refusing, and says what is missing when it does not.
func acceptedExecution(t *testing.T, wf isolatedWorkflow, id string) (bool, string) {
	t.Helper()

	job := wf.Jobs[id]

	// A job that calls another workflow executes nothing itself. It may only
	// hand a credential to a workflow this package also reads.
	if strings.TrimSpace(job.Uses) != "" {
		if !strings.HasPrefix(strings.TrimSpace(job.Uses), "./") {
			return false, "it hands a credential to " + job.Uses +
				", a workflow outside this repository, whose jobs none of these guards read"
		}
		return true, ""
	}

	if reason, ok := executionSurfaceIsPinned(t, wf, id); !ok {
		return false, reason
	}
	// A ref pin constrains the TREE. These constrain what is run from it, and
	// they apply to both acceptance paths below: an ancestry refusal proves the
	// commit was merged, not that the program came from it.
	if reason, ok := executionInputsArePinned(t, wf, wf.Jobs[id]); !ok {
		return false, reason
	}

	// (3) A refusal whose behaviour a test establishes, placed before anything
	// executes. The registry decides, not the script's wording.
	//
	// A valid proof does NOT end the question. It establishes that the commit
	// the job starts from was merged; it says nothing about a checkout later in
	// the same job, or inside an action it invokes. So this records that the
	// proof is present and keeps going -- there is no early return.
	provenByRefusal := false
	if index, reason, found := verifiedRefusalIndex(wf.name, id, job.Steps); found {
		if first, executes := firstExecutingStepIndex(job, index); executes && first < index {
			return false, "its refusal is at step " + job.Steps[index].Name +
				" but step " + job.Steps[first].Name + " executes repository code before it"
		}
		provenByRefusal = true
	} else if reason != "" {
		return false, reason
	}

	steps, unreadable := stepsIncludingLocalActions(t, job)
	if len(unreadable) > 0 {
		return false, "it invokes " + strings.Join(unreadable, ", ") +
			", whose execution this guard cannot read"
	}

	// Every checkout, including the ones after a refusal and the ones inside a
	// local action. A refusal covers the commit the job began on; a later
	// checkout selects a different tree, which nothing has proved anything
	// about. The ONE checkout a refusal does cover is the bare one it was
	// written for -- the step before it, which has no `ref:` and whose tree the
	// refusal then validates.
	for i, step := range steps {
		if !strings.Contains(step.Uses, "actions/checkout@") {
			continue
		}
		ref, _ := step.With["ref"].(string)
		if strings.TrimSpace(ref) == theDefaultBranch {
			continue
		}
		if provenByRefusal && i == 0 && strings.TrimSpace(ref) == "" {
			continue // the tree the refusal validates
		}
		which := "no ref: at all (which means the triggering ref)"
		if ref != "" {
			which = "ref: " + ref
		}
		where := "its checkout"
		if step.Name != "" {
			where = "the checkout in step " + step.Name
		}
		return false, where + " has " + which +
			", which no refusal in this job covers"
	}
	return true, ""
}

// everyCheckoutPinsTheDefaultBranch requires each checkout in the list to name
// the default branch outright. The list includes the steps of any local action
// the job invokes, because a pinned checkout beside an unpinned nested one is
// not pinned. An ABSENT ref is not a pin: it means the triggering ref, which
// for a dispatch, a tag push or a caller is the party's choice.
func everyCheckoutPinsTheDefaultBranch(steps []isolatedStep) (string, bool) {
	for _, step := range steps {
		if !strings.Contains(step.Uses, "actions/checkout@") {
			continue
		}
		ref, _ := step.With["ref"].(string)
		if strings.TrimSpace(ref) == theDefaultBranch {
			continue
		}
		which := "no ref: at all (which means the triggering ref)"
		if ref != "" {
			which = "ref: " + ref
		}
		where := "its checkout"
		if step.Name != "" {
			where = "the checkout in step " + step.Name
		}
		return where + " has " + which +
			", and this job carries no refusal whose behaviour a test establishes", false
	}
	return "", true
}

// executionSurfaceIsPinned checks the places a job runs code from that are not
// a step's script: a service or job container image, which selects an
// executable, and which a guard reading only `steps` never saw.
func executionSurfaceIsPinned(t *testing.T, wf isolatedWorkflow, id string) (string, bool) {
	t.Helper()

	raw, ok := wf.raw[id]
	require.True(t, ok, "job %q was not captured verbatim", id)

	var surfaces struct {
		Container yaml.Node            `yaml:"container"`
		Services  map[string]yaml.Node `yaml:"services"`
	}
	require.NoError(t, raw.Decode(&surfaces), "job %q", id)

	check := func(where string, node yaml.Node) (string, bool) {
		if node.Kind == 0 {
			return "", true
		}
		var rendered strings.Builder
		require.NoError(t, yaml.NewEncoder(&rendered).Encode(node), "job %q %s", id, where)
		for _, root := range partyChosenContexts {
			reads, err := contextReadsUnder(rendered.String(), root)
			require.NoError(t, err, "job %q %s", id, where)
			if len(reads) > 0 {
				return "its " + where + " is built from " + strings.Join(reads, ", ") +
					", which the triggering party supplies, so it selects an executable this repository has not reviewed", false
			}
		}
		return "", true
	}

	if reason, ok := check("container", surfaces.Container); !ok {
		return reason, false
	}
	for name, node := range surfaces.Services {
		if reason, ok := check("service "+name, node); !ok {
			return reason, false
		}
	}
	return "", true
}

// verifiedRefusal names a refusal step whose behaviour a test in this package
// ESTABLISHES by running it against real repositories.
type verifiedRefusal struct {
	workflow, job, step string
	// verifiedBy is the test that lifts this script out of the workflow and
	// executes it. It is named so the registry cannot claim coverage that does
	// not exist: TestEveryVerifiedRefusalIsReallyVerified requires the step to
	// exist and the test to be present in this package.
	verifiedBy string
}

// verifiedRefusals is the whole set of refusals a credential-bearing job may
// rest on.
//
// Reading a script cannot establish what it does. A check that recognises the
// command's shape -- that it is not commented out, not skipped, not followed by
// `|| true` -- is still reading text, and text can satisfy any list of shapes
// without the commit being reachable. So shapes are not recognised at all: the
// only refusals that count are the ones a test EXECUTES, and the acceptance
// path consults this registry rather than the script.
//
// That makes the obligation concrete. A new credential-bearing job cannot be
// accepted on a hand-written proof; it is accepted once its refusal is executed
// by a test here, against repositories where the commit genuinely is or is not
// reachable.
var verifiedRefusals = []verifiedRefusal{
	{
		workflow:   "version-tag.yml",
		job:        "tag",
		step:       "select the commit that passed CI, or refuse",
		verifiedBy: "TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch",
	},
	{
		workflow:   "go-service-release.yml",
		job:        "goreleaser",
		step:       "require the tag to be on the default branch, or refuse",
		verifiedBy: "TestAReleaseIsAdmittedOnlyFromTheRepositorysOwnDefaultBranch",
	},
}

// refusalIsUnconditionalAndFatal reports whether a registered refusal will
// actually run and actually stop the job.
//
// Being in the registry says a test executes the SCRIPT. It says nothing about
// whether the step runs: a `if:` on it, or `continue-on-error: true`, and the
// job proceeds past an unmerged commit with the credential while the script
// itself is unchanged and its test still passes. So any step control that can
// skip it or tolerate its failure is itself a refusal of the witness.
func refusalIsUnconditionalAndFatal(step isolatedStep) (string, bool) {
	if gate := strings.TrimSpace(step.If); gate != "" {
		return "it carries `if: " + gate + "`, so it does not run unconditionally", false
	}
	if step.ContinueOnError {
		return "it carries `continue-on-error: true`, so its failure does not stop the job", false
	}
	return "", true
}

// verifiedRefusalIndex returns the index of a step whose refusal is established
// by an executed test AND will run fatally, for this workflow and job.
func verifiedRefusalIndex(workflow, job string, steps []isolatedStep) (int, string, bool) {
	for _, known := range verifiedRefusals {
		if known.workflow != workflow || known.job != job {
			continue
		}
		for i, step := range steps {
			if step.Name != known.step {
				continue
			}
			if reason, ok := refusalIsUnconditionalAndFatal(step); !ok {
				return 0, "its registered refusal is not a witness: " + reason, false
			}
			return i, "", true
		}
	}
	return 0, "", false
}

// firstExecutingStepIndex finds the first step that could run repository code:
// any script other than the proof itself, or any action that is not a checkout.
// A checkout places code in the tree without running it, which is why the
// proof is allowed to come after one.
func firstExecutingStepIndex(job isolatedJob, proof int) (int, bool) {
	for i, step := range job.Steps {
		if i == proof {
			continue
		}
		if strings.TrimSpace(step.Run) != "" {
			return i, true
		}
		if uses := strings.TrimSpace(step.Uses); uses != "" && !strings.Contains(uses, "actions/checkout@") {
			return i, true
		}
	}
	return 0, false
}

// executionInputsArePinned checks the inputs that select WHAT RUNS, which a ref
// pin says nothing about: the program a step executes, the repository a
// checkout reads, and the same two inside any local action the job invokes.
//
// A pinned checkout beside `run: eval "$TASK"` with `TASK` from an input is not
// pinned -- the tree is this repository's and the program is the caller's.
func executionInputsArePinned(t *testing.T, wf isolatedWorkflow, job isolatedJob) (string, bool) {
	t.Helper()

	steps, unreadable := stepsIncludingLocalActions(t, job)
	if len(unreadable) > 0 {
		return "it invokes " + strings.Join(unreadable, ", ") +
			", whose execution this guard cannot read", false
	}

	// Inherited environment counts: a party-chosen value set at workflow or job
	// level is in scope for every script in the job, and reads as if it were
	// the step's own.
	inherited := map[string]string{}
	for name, value := range wf.Env {
		inherited[name] = value
	}
	for name, value := range job.Env {
		inherited[name] = value
	}

	// Provenance travels. A step can write a party-chosen value into
	// $GITHUB_ENV under a fresh name, and the next step executes that name --
	// so taint is carried forward across steps rather than rebuilt per step.
	tainted := map[string]bool{}
	for name, value := range inherited {
		if partyChosen(t, value) {
			tainted[name] = true
		}
	}

	for _, step := range steps {
		where := "step " + step.Name
		if step.Name == "" {
			where = "step `uses: " + step.Uses + "`"
		}
		scoped := map[string]string{}
		for name, value := range inherited {
			scoped[name] = value
		}
		for name, value := range step.Env {
			scoped[name] = value
			if partyChosen(t, value) {
				tainted[name] = true
			}
		}

		// Anything this step already carries forward.
		for name := range tainted {
			if scriptExecutesEnvName(step.Run, name) {
				return where + " runs a program from $" + name +
					", whose value the triggering party supplies", false
			}
		}

		// And what it writes onward. A write this guard cannot attribute is a
		// refusal rather than an assumption.
		exported, unresolved := environmentExports(step.Run)
		if unresolved != "" {
			return where + " writes " + unresolved +
				" into $GITHUB_ENV in a form whose provenance this guard cannot " +
				"establish", false
		}
		for name, from := range exported {
			for _, source := range from {
				if tainted[source] {
					tainted[name] = true
				}
			}
		}

		// The EFFECTIVE shell: the step's own `shell:`, else the job's
		// `defaults.run.shell`, else the workflow's, else the platform
		// default. It was read from `with.shell`, which is not where it
		// lives -- so `shell: python` with a script that executes an
		// environment variable was read as if it were bash and accepted.
		if shell := effectiveShell(wf, job, step); !readableShells[shell] {
			return where + " runs under shell " + shell +
				", whose execution forms this guard does not read", false
		}

		// The program a step runs.
		for _, root := range partyChosenContexts {
			reads, err := contextReadsUnder(step.Run, root)
			require.NoError(t, err, where)
			if len(reads) > 0 {
				return where + " runs a program built from " + strings.Join(reads, ", ") +
					", which the triggering party supplies", false
			}
			for key, text := range scoped {
				reads, err := contextReadsUnder(text, root)
				require.NoError(t, err, where)
				if len(reads) > 0 && scriptExecutesEnvName(step.Run, key) {
					return where + " runs a program built from " + strings.Join(reads, ", ") +
						" through $" + key + ", which the triggering party supplies", false
				}
			}
		}

		if !strings.Contains(step.Uses, "actions/checkout@") {
			continue
		}
		// The repository a checkout reads. Absent means this one; anything
		// else, including an expression, selects another tree.
		if repository, ok := step.With["repository"].(string); ok && strings.TrimSpace(repository) != "" {
			return where + " checks out repository " + repository +
				" rather than this one", false
		}
	}
	return "", true
}

// executionPositions are the forms that turn a VALUE into a PROGRAM. The
// distinction matters and is the whole reason this is not simply "the script
// mentions the variable": a party-chosen value compared against an authority
// is being CHECKED, which is what a refusal does, while the same value reaching
// `eval` is being RUN.
//
// Stated as a limit rather than left implicit: this reads the shapes a script
// uses to execute a variable. A script that reached the same end by a route not
// listed here would not be seen, and the covering protection in that case is
// that a credential-bearing job must also pin its tree or carry a refusal --
// not this check alone.
var executionPositions = []string{
	"eval", "sh -c", "bash -c", "zsh -c", "python -c", "python3 -c", "node -e",
	"source ", ". $", ". \"$", "xargs", "| sh", "| bash",
}

// scriptExecutesEnvName reports whether a script runs the value of an
// environment variable as a program, rather than reading it as data.
func scriptExecutesEnvName(script, name string) bool {
	reference := []string{"$" + name, "${" + name}
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		mentions := false
		for _, form := range reference {
			if strings.Contains(trimmed, form) {
				mentions = true
			}
		}
		if !mentions {
			continue
		}
		for _, position := range executionPositions {
			if strings.Contains(trimmed, position) {
				return true
			}
		}
		// A line whose first word is the variable runs it.
		for _, form := range reference {
			if strings.HasPrefix(trimmed, form) || strings.HasPrefix(trimmed, "\""+form) {
				return true
			}
		}
	}
	return false
}

// readableShells are the interpreters whose execution forms this guard reads.
// Anything else -- python, pwsh, node, a custom `shell:` command -- refuses,
// because "which constructs run a string" is a property of the interpreter.
var readableShells = map[string]bool{"bash": true, "sh": true}

// effectiveShell resolves the interpreter a step actually runs under: its own
// `shell:`, else the job's `defaults.run.shell`, else the workflow's, else the
// platform default, which on the runners this repository uses is bash.
func effectiveShell(wf isolatedWorkflow, job isolatedJob, step isolatedStep) string {
	for _, candidate := range []string{
		step.Shell, job.Defaults.Run.Shell, wf.Defaults.Run.Shell,
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "bash"
}

// partyChosen reports whether a value reads from a context the triggering party
// supplies.
func partyChosen(t *testing.T, value string) bool {
	t.Helper()
	for _, root := range partyChosenContexts {
		reads, err := contextReadsUnder(value, root)
		require.NoError(t, err)
		if len(reads) > 0 {
			return true
		}
	}
	return false
}

// githubEnvWrite matches the shapes a script uses to export a variable for
// later steps: `NAME=value >> $GITHUB_ENV` and the `echo "NAME=value"` form.
var githubEnvWrite = regexp.MustCompile(`(?m)^\s*(?:echo|printf)?\s*"?([A-Za-z_][A-Za-z0-9_]*)=([^"\n]*)"?\s*>>\s*"?\$\{?GITHUB_ENV`)

// environmentExports returns, for each name a script writes into $GITHUB_ENV,
// the variable names its value was built from -- and names any write whose
// provenance cannot be read, so that refuses instead of being assumed clean.
func environmentExports(script string) (map[string][]string, string) {
	exported := map[string][]string{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "GITHUB_ENV") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		groups := githubEnvWrite.FindStringSubmatch(line)
		if groups == nil {
			// It writes to the environment file in a shape this cannot
			// attribute -- a heredoc, a loop, a tool call.
			return nil, "a value"
		}
		var sources []string
		for _, reference := range regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(groups[2], -1) {
			sources = append(sources, reference[1])
		}
		exported[groups[1]] = sources
	}
	return exported, ""
}

// trustedWorkflowSources: the triggers for which GitHub takes the WORKFLOW FILE
// from a ref only a writer of this repository can set.
//
// This is a provenance separate from the checkout's, and pinning the checkout
// does not supply it.
//
//   - `push` qualifies only when its filter admits the default branch alone.
//   - `workflow_run` and `schedule` take the file from the default branch.
//   - `workflow_call` takes it from a ref of THIS repository, which only a
//     writer can create.
//
// Everything else -- `pull_request` and its relatives, `workflow_dispatch`,
// `release`, `create`, `delete` -- takes the file from a ref the triggering
// party influences.
func workflowSourceIsTrusted(t *testing.T, on yaml.Node) (bool, []string) {
	t.Helper()

	var untrusted []string
	for _, trigger := range triggers(on) {
		switch trigger {
		case "workflow_run", "schedule", "workflow_call":
			continue
		case "push":
			node, ok := triggerNode(on, "push")
			if !ok || node.Kind != yaml.MappingNode {
				untrusted = append(untrusted, "push (every branch and tag)")
				continue
			}
			var filter pullRequestTrigger
			require.NoError(t, node.Decode(&filter))
			if len(filter.Branches) == 1 && filter.Branches[0] == theDefaultBranch &&
				len(filter.Tags) == 0 {
				continue
			}
			untrusted = append(untrusted, "push (not confined to "+theDefaultBranch+")")
		default:
			untrusted = append(untrusted, trigger)
		}
	}
	return len(untrusted) == 0, untrusted
}
