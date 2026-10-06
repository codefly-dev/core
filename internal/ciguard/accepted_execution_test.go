package ciguard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Trigger identity cannot establish which code a job executes, and every
// exemption written in those terms had a concrete accepted construction: a
// secret-bearing job on `release: published` with a tag at unmerged code; one
// on `push` to a branch named `unmerged`; one on `schedule` checking out the
// literal `refs/pull/8/head`; a dispatch job whose only credential was the
// built-in write token. Each was waved through by a comment naming a guard
// whose scope did not reach it.
//
// So there are no exemptions. A job holding a credential is accepted only if
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
	if reason, ok := executionInputsArePinned(t, wf.Jobs[id]); !ok {
		return false, reason
	}

	// (3) An ancestry refusal before anything executes.
	if index, found := ancestryProofIndex(job); found {
		if first, executes := firstExecutingStepIndex(job, index); executes && first < index {
			return false, "its ancestry proof is at step " + job.Steps[index].Name +
				" but step " + job.Steps[first].Name + " executes repository code before it"
		}
		return true, ""
	}

	// (2) Otherwise every checkout must name the default branch outright. An
	// ABSENT ref is not pinned: it means the triggering ref, which for a
	// dispatch, a tag push or a caller is the party's choice.
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/checkout@") {
			continue
		}
		ref, _ := step.With["ref"].(string)
		if strings.TrimSpace(ref) != theDefaultBranch {
			which := "no ref: at all (which means the triggering ref)"
			if ref != "" {
				which = "ref: " + ref
			}
			return false, "its checkout has " + which +
				", and it carries no `git merge-base --is-ancestor` refusal either"
		}
	}
	return true, ""
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

// ancestryProofIndex finds a step that actually REFUSES on unmerged code.
//
// It used to search for the command's text. A substring establishes none of
// the things that make a refusal a refusal, and every one of these was
// accepted: the command inside a comment, the step carrying `if: false`, the
// command followed by `|| true`, the step carrying `continue-on-error: true`,
// and a command comparing two branches rather than the selected commit. So the
// shape is read instead -- the step must run, its failure must stop the job,
// and its operands must be the commit under consideration and a remote branch.
//
// This is a structural check, and it is deliberately not the only one: the
// release and tag refusals are also executed against real repositories, in
// version_tag_selection_test.go and release_admission_test.go. A guard that
// reads a script cannot establish what the script does.
func ancestryProofIndex(job isolatedJob) (int, bool) {
	for i, step := range job.Steps {
		if stepRefusesUnmergedCommits(step) {
			return i, true
		}
	}
	return 0, false
}

// selectedCommitOperands are the ways a workflow here names the commit it is
// deciding about. A refusal that names none of them is comparing something
// else.
var selectedCommitOperands = []string{"$GITHUB_SHA", "${GITHUB_SHA}", "$SHA", "${SHA}"}

// stepRefusesUnmergedCommits reports whether this step is a refusal that runs,
// stops the job, and asks about the right commit.
func stepRefusesUnmergedCommits(step isolatedStep) bool {
	command := ""
	for _, line := range strings.Split(step.Run, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // a comment runs nothing
		}
		if strings.Contains(trimmed, "merge-base --is-ancestor") {
			command = trimmed
			break
		}
	}
	if command == "" {
		return false
	}
	// A step that may be skipped proves nothing on the runs where it is.
	if strings.TrimSpace(step.If) != "" {
		return false
	}
	// A failure that is tolerated is not a refusal.
	if step.ContinueOnError {
		return false
	}
	for _, swallow := range []string{"|| true", "|| :", "|| exit 0", "continue-on-error"} {
		if strings.Contains(command, swallow) {
			return false
		}
	}
	// It has to be asking about the commit under consideration, against a
	// branch from the remote rather than a local name anything could move.
	named := false
	for _, operand := range selectedCommitOperands {
		if strings.Contains(command, operand) {
			named = true
		}
	}
	return named && strings.Contains(command, "origin/")
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
func executionInputsArePinned(t *testing.T, job isolatedJob) (string, bool) {
	t.Helper()

	steps, unreadable := stepsIncludingLocalActions(t, job)
	if len(unreadable) > 0 {
		return "it invokes " + strings.Join(unreadable, ", ") +
			", whose execution this guard cannot read", false
	}

	for _, step := range steps {
		where := "step " + step.Name
		if step.Name == "" {
			where = "step `uses: " + step.Uses + "`"
		}

		// The program a step runs.
		for _, root := range partyChosenContexts {
			reads, err := contextReadsUnder(step.Run, root)
			require.NoError(t, err, where)
			if len(reads) > 0 {
				return where + " runs a program built from " + strings.Join(reads, ", ") +
					", which the triggering party supplies", false
			}
			for key, text := range step.Env {
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
var executionPositions = []string{"eval", "sh -c", "bash -c", "source ", ". $", ". \"$"}

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
