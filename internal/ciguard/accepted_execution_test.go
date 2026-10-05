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

// partyChosenContexts are the context roots whose values the triggering party
// supplies. An execution surface built from one of these runs code this
// repository has not reviewed.
var partyChosenContexts = []string{"inputs", "github.event"}

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

// ancestryProofIndex finds the step carrying the refusal.
func ancestryProofIndex(job isolatedJob) (int, bool) {
	for i, step := range job.Steps {
		if strings.Contains(step.Run, "merge-base --is-ancestor") {
			return i, true
		}
	}
	return 0, false
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
