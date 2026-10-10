package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both the judgement and the executing test use this binding. A test's name
// alone is no evidence: it must lift THIS workflow, job and step. The lifting
// helper selects its binding from the running test's name, not a caller-supplied
// label, so borrowing a different test cannot certify a new refusal.
type executionProof struct{ workflow, job, step string }

func executionProofFor(test string) (executionProof, bool) {
	switch test {
	case "TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch":
		return executionProof{versionTagWorkflow, "tag", selectionStepName}, true
	case "TestAReleaseIsAdmittedOnlyFromTheRepositorysOwnDefaultBranch", "TestNeutralisingTheReleaseRefusalIsVisible":
		return executionProof{"go-service-release.yml", "goreleaser", releaseRefusalStep}, true
	case "TestAServiceImageIsPublishedOnlyFromTheRepositorysOwnDefaultBranch":
		return executionProof{publishWorkflow, publishJob, publishRefusalStep}, true
	case "TestDispatchPublisherFixtureRefusesUnmergedCommits":
		return executionProof{"dispatch-publisher-fixture.yml", "publish", publishRefusalStep}, true
	}
	return executionProof{}, false
}

func executingTestCovers(template jobTemplate, step isolatedStep) bool {
	proof, ok := executionProofFor(template.executedBy)
	return ok && proof.workflow == template.workflow && proof.job == template.job && proof.step == step.Name && step.Run != ""
}

func executionTestStep(t *testing.T) isolatedStep {
	t.Helper()
	test, _, _ := strings.Cut(t.Name(), "/")
	proof, ok := executionProofFor(test)
	require.True(t, ok, "%s has no workflow/job/step execution binding", test)
	path := filepath.Join(repoRoot(t), ".github/workflows", proof.workflow)
	if proof.workflow == "dispatch-publisher-fixture.yml" {
		path = filepath.Join(repoRoot(t), "internal/ciguard/testdata/dispatch-publisher.yml")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	wf := parseIsolatedDocument(t, string(raw), proof.workflow)
	var found []isolatedStep
	for _, step := range wf.Jobs[proof.job].Steps {
		if step.Name == proof.step {
			found = append(found, step)
		}
	}
	require.Len(t, found, 1, "%s must lift its exact job's one refusal", test)
	require.NotEmpty(t, found[0].Run)
	return found[0]
}
