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

// acceptedExecution decides whether a credential-bearing job establishes what
// it executes.
//
// ONE ordered pass over every step the job runs -- its own and those of any
// local action it invokes -- carrying two things: the set of names whose value
// the triggering party supplies, and whether a refusal has succeeded. Each step
// is then checked against every sink a value can become a program through, and
// anything this pass cannot follow REFUSES rather than being assumed clean.
//
// Rules that enumerate command shapes were replaced because each round found a
// sink the list did not have. The sinks are: a script, every input of every
// action, $GITHUB_ENV, $GITHUB_PATH, $BASH_ENV, a matrix, an expression alias,
// and a shell assignment. Dominance is the other half: after a refusal, no step
// may run unless the refusal succeeded, and no step may change the tree away
// from the commit the refusal proved.
func acceptedExecution(t *testing.T, wf isolatedWorkflow, id string) (bool, string) {
	t.Helper()

	job := wf.Jobs[id]

	// A job that calls another workflow executes nothing itself.
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

	// A matrix reaches every step that reads `matrix.*`.
	if job.Strategy.Kind != 0 {
		var rendered strings.Builder
		require.NoError(t, yaml.NewEncoder(&rendered).Encode(job.Strategy))
		if partyChosen(t, rendered.String()) {
			return false, "its strategy/matrix is built from a value the triggering " +
				"party supplies, so every step reading matrix.* reads that value"
		}
	}

	steps, unreadable := stepsIncludingLocalActions(t, job)
	if len(unreadable) > 0 {
		return false, "it invokes " + strings.Join(unreadable, ", ") +
			", whose execution this guard cannot read"
	}

	// Where the refusal sits in the FULL list, so dominance covers nested
	// actions as well as the job's own steps.
	refusal := -1
	if _, reason, found := verifiedRefusalIndex(wf.name, id, job.Steps); found {
		for i, step := range steps {
			if step.Name != "" && step.Name == job.Steps[indexOfNamed(job.Steps, step.Name)].Name &&
				isRegisteredRefusal(wf.name, id, step.Name) {
				refusal = i
				break
			}
		}
	} else if reason != "" {
		return false, reason
	}

	tainted := map[string]bool{}
	for name, value := range wf.Env {
		if partyChosen(t, value) {
			tainted[name] = true
		}
	}
	for name, value := range job.Env {
		if partyChosen(t, value) {
			tainted[name] = true
		}
	}

	for i, step := range steps {
		where := "step " + step.Name
		if step.Name == "" {
			where = "step `uses: " + step.Uses + "`"
		}

		// --- dominance -------------------------------------------------
		if refusal >= 0 && i > refusal {
			if gate := strings.TrimSpace(step.If); gate != "" {
				return false, where + " carries `if: " + gate + "` after the refusal, " +
					"so its execution does not require the refusal to have succeeded"
			}
			if step.ContinueOnError {
				return false, where + " tolerates its own failure after the refusal"
			}
		}
		if refusal >= 0 && i < refusal {
			if gate := strings.TrimSpace(step.If); gate != "" {
				return false, where + " carries `if: " + gate + "` before the refusal"
			}
		}

		// --- the tree it changes, wherever it is ----------------------
		// A shell checkout is a tree change the action-input rule never saw,
		// and it is a tree change whether or not a refusal precedes it: a job
		// whose `actions/checkout` names the default branch has still selected
		// another tree once a script moves it. So every one of these binds the
		// proved commit or the default branch by name, or refuses.
		for _, verb := range []string{"git checkout", "git switch", "git reset", "git worktree"} {
			if !strings.Contains(step.Run, verb) {
				continue
			}
			bound := strings.Contains(step.Run, theDefaultBranch)
			for _, operand := range selectedCommitOperands {
				if strings.Contains(step.Run, operand) {
					bound = true
				}
			}
			if !bound {
				return false, where + " runs `" + verb +
					"` without binding the commit a refusal proved or the default branch"
			}
		}

		// --- the interpreter -------------------------------------------
		if shell := effectiveShell(wf, job, step); !readableShells[shell] {
			return false, where + " runs under shell " + shell +
				", whose execution forms this guard does not read"
		}

		// --- sources ---------------------------------------------------
		for name, value := range step.Env {
			if partyChosen(t, value) {
				tainted[name] = true
			}
			for carried := range tainted {
				if referencesName(value, carried) {
					tainted[name] = true
				}
			}
		}

		// --- sinks -----------------------------------------------------
		if reason, ok := sinksAreClean(t, where, step, tainted); !ok {
			return false, reason
		}

		// --- propagation ----------------------------------------------
		if reason, ok := propagate(step.Run, tainted); !ok {
			return false, where + " " + reason
		}

		// --- the tree it selects --------------------------------------
		coveredByRefusal := refusal == i+1
		if reason, ok := checkoutSelectsAPermittedTree(step, where, coveredByRefusal); !ok {
			return false, reason
		}
	}
	return true, ""
}

// checkoutSelectsAPermittedTree is the checkout rule, called from the one pass
// above with the FULL step list -- the job's own and every local action's -- so
// a nested checkout is judged by the same function as a top-level one.
//
// coveredByRefusal marks the single checkout a refusal validates: the bare one
// it immediately follows. An explicit ref is not that checkout, because the
// refusal proves a commit, not whatever else was named.
func checkoutSelectsAPermittedTree(step isolatedStep, where string, coveredByRefusal bool) (string, bool) {
	if !strings.Contains(step.Uses, "actions/checkout@") {
		return "", true
	}
	if repository, ok := step.With["repository"].(string); ok && strings.TrimSpace(repository) != "" {
		return where + " checks out repository " + repository + " rather than this one", false
	}
	ref, _ := step.With["ref"].(string)
	if strings.TrimSpace(ref) == theDefaultBranch {
		return "", true
	}
	if coveredByRefusal && strings.TrimSpace(ref) == "" {
		return "", true
	}
	which := "no ref: at all (which means the triggering ref)"
	if ref != "" {
		which = "ref: " + ref
	}
	return where + " has " + which + ", which no refusal in this job covers", false
}

// everyCheckoutPinsTheDefaultBranch applies that rule to a whole step list,
// with no refusal in play.
func everyCheckoutPinsTheDefaultBranch(steps []isolatedStep) (string, bool) {
	for _, step := range steps {
		where := "its checkout"
		if step.Name != "" {
			where = "the checkout in step " + step.Name
		}
		if reason, ok := checkoutSelectsAPermittedTree(step, where, false); !ok {
			return reason, false
		}
	}
	return "", true
}

// selectedCommitOperands are the ways a workflow here names the commit under
// consideration. A tree change after a refusal must bind one of them.
var selectedCommitOperands = []string{"$GITHUB_SHA", "${GITHUB_SHA}", "$SHA", "${SHA}"}

// indexOfNamed finds a step by name, or 0.
func indexOfNamed(steps []isolatedStep, name string) int {
	for i, step := range steps {
		if step.Name == name {
			return i
		}
	}
	return 0
}

// isRegisteredRefusal reports whether this step name is the registered refusal
// for this workflow and job.
func isRegisteredRefusal(workflow, job, step string) bool {
	for _, known := range verifiedRefusals {
		if known.workflow == workflow && known.job == job && known.step == step {
			return true
		}
	}
	return false
}

// referencesName reports whether text reads a variable, in any of the spellings
// a workflow uses: a shell expansion or an expression alias.
func referencesName(text, name string) bool {
	for _, form := range []string{"$" + name, "${" + name, "env." + name} {
		if strings.Contains(text, form) {
			return true
		}
	}
	return false
}

// sinksAreClean checks every place a value can become a program.
func sinksAreClean(t *testing.T, where string, step isolatedStep, tainted map[string]bool) (string, bool) {
	t.Helper()

	sinks := map[string]string{"run:": step.Run}
	for key, value := range step.With {
		if text, ok := value.(string); ok {
			sinks["with."+key] = text
		}
	}

	for label, text := range sinks {
		if partyChosen(t, text) {
			return where + " passes a triggering-party value to " + label, false
		}
		for name := range tainted {
			// An expression alias renders the value INTO the text, so it is a
			// flow wherever it appears -- a script included.
			if strings.Contains(text, "${{ env."+name) || strings.Contains(text, "${{env."+name) {
				return where + " expands $" + name + " into " + label +
					", whose value the triggering party supplies", false
			}
			if label == "run:" {
				if scriptExecutesEnvName(text, name) {
					return where + " runs a program from $" + name +
						", whose value the triggering party supplies", false
				}
				continue
			}
			if referencesName(text, name) {
				return where + " passes $" + name + " to " + label +
					", whose value the triggering party supplies", false
			}
		}
	}

	// Sinks whose flow cannot be followed at all, whatever they carry.
	for marker, why := range map[string]string{
		"GITHUB_PATH": "appends to $GITHUB_PATH, after which a later step runs a " +
			"program by a name this cannot attribute",
		"BASH_ENV": "sets $BASH_ENV, which loads a script into every later shell",
		"chmod +x": "makes a file executable, and what it wrote there cannot be followed here",
	} {
		if strings.Contains(step.Run, marker) {
			return where + " " + why, false
		}
	}
	return "", true
}

// shellAssignment matches `NAME=value` and `export NAME=value` at the start of
// a line -- a shell local, which carries a value onward exactly as an env var
// does.
var shellAssignment = regexp.MustCompile(`(?m)^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// propagate carries taint through a script: shell assignments and writes to the
// environment file. A write it cannot attribute refuses.
func propagate(script string, tainted map[string]bool) (string, bool) {
	exported, unresolved := environmentExports(script)
	if unresolved != "" {
		return "writes " + unresolved + " into $GITHUB_ENV in a form whose " +
			"provenance this guard cannot establish", false
	}

	// Shell locals first: `ALIAS="$TASK"` then `NEXT=$ALIAS` must both carry.
	for pass := 0; pass < 3; pass++ {
		for _, assignment := range shellAssignment.FindAllStringSubmatch(script, -1) {
			name, value := assignment[1], assignment[2]
			if strings.Contains(value, "GITHUB_ENV") {
				continue
			}
			for carried := range tainted {
				if referencesName(value, carried) {
					tainted[name] = true
				}
			}
		}
		for name, sources := range exported {
			for _, source := range sources {
				if tainted[source] {
					tainted[name] = true
				}
			}
		}
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

	// A matrix is a sink too: its values reach every step that reads
	// `matrix.*`, and a matrix built from a party-chosen context supplies all
	// of them.
	if job.Strategy.Kind != 0 {
		var rendered strings.Builder
		require.NoError(t, yaml.NewEncoder(&rendered).Encode(job.Strategy))
		if partyChosen(t, rendered.String()) {
			return "its strategy/matrix is built from a value the triggering party " +
				"supplies, so every step reading matrix.* reads that value", false
		}
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

		// ONE analysis over every sink a step has. Command-string patterns
		// cannot enumerate them: an action input (`with: script:`), a file
		// written and made executable, a directory appended to $GITHUB_PATH
		// and then invoked by name, and a matrix value are each a way for a
		// value to become a program. So each sink is checked for a
		// party-chosen context or a tainted name, and a flow this cannot
		// follow refuses.
		sinks := map[string]string{"run:": step.Run}
		for key, value := range step.With {
			if text, ok := value.(string); ok {
				sinks["with."+key] = text
			}
		}
		for label, text := range sinks {
			if partyChosen(t, text) {
				return where + " passes a triggering-party value to " + label, false
			}
			for name := range tainted {
				if label == "run:" {
					if scriptExecutesEnvName(text, name) {
						return where + " runs a program from $" + name +
							", whose value the triggering party supplies", false
					}
					continue
				}
				// An action input is consumed by the action, not by a shell,
				// so any mention of a tainted value is a flow into it.
				if strings.Contains(text, "$"+name) || strings.Contains(text, "${"+name) ||
					strings.Contains(text, "env."+name) {
					return where + " passes $" + name + " to " + label +
						", whose value the triggering party supplies", false
				}
			}
		}

		// Sinks whose flow cannot be followed at all.
		for marker, why := range map[string]string{
			"GITHUB_PATH": "appends a directory to $GITHUB_PATH, after which a later " +
				"step runs a program by name that this cannot attribute",
			"chmod +x": "makes a file executable, and what it wrote into that file " +
				"cannot be followed here",
		} {
			if strings.Contains(step.Run, marker) {
				return where + " " + why, false
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
	// No shell named: the RUNNER decides, so the runner has to be read. A
	// windows runner defaults to PowerShell, where `Invoke-Expression
	// $env:TASK` runs a value -- read as bash, that construct is invisible.
	// A runner this does not recognise yields an unreadable shell, which
	// refuses.
	return platformShell(job)
}

// linuxRunners are the labels whose default shell is bash. Anything else --
// windows, macos, a self-hosted label, a matrix expression -- is not assumed.
var linuxRunners = map[string]bool{
	"ubuntu-latest": true, "ubuntu-24.04": true, "ubuntu-22.04": true, "ubuntu-20.04": true,
}

// platformShell returns the runner's default interpreter, or a name no readable
// shell matches when the runner cannot be identified.
func platformShell(job isolatedJob) string {
	if job.Container.Kind != 0 {
		return "bash" // a linux container image; its default is sh/bash
	}
	switch job.RunsOn.Kind {
	case yaml.ScalarNode:
		if linuxRunners[strings.TrimSpace(job.RunsOn.Value)] {
			return "bash"
		}
		return "the default shell of runner " + job.RunsOn.Value
	case yaml.SequenceNode:
		for _, entry := range job.RunsOn.Content {
			if linuxRunners[strings.TrimSpace(entry.Value)] {
				return "bash"
			}
		}
	}
	return "an unidentified runner's default shell"
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
