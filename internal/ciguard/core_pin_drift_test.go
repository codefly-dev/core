package ciguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The core-pin drift gate is required in NINE OTHER REPOSITORIES, by a context
// NAME, written into each one's branch protection where nothing in this
// repository can test it. That makes the name a contract, and it is a contract
// this file cannot simply read off its own workflow: GitHub names a reusable
// workflow's check `<caller job id> / <reusable job id>`, and the caller's half
// is chosen by a repository core cannot see.
//
// What core can hold is that the snippet it tells consumers to paste produces
// the context it tells administrators to require. Those are three strings in
// two files — the caller's job id and the reusable job's id, and the context
// name in the prose — and the failure when they disagree is the worst one
// available: protection requiring a name nothing reports holds every merge at
// "Expected — waiting for status to be reported" until somebody with admin
// access notices. The same shape as the set pinned in branchprotection_test.go,
// for the same reason.
//
// This exists because codefly-dev/core#891 is explicit that several findings in
// this programme of work were a scanner that ran, failed, and blocked nothing.
// A gate whose context name is wrong is that, with extra steps.

const (
	corePinDriftWorkflow = ".github/workflows/core-pin-drift.yml"
	corePinDriftDoc      = "docs/core-pin-drift.md"
)

// readWorkflowFile parses one workflow by repository-relative path.
func readWorkflowFile(t *testing.T, relative string) workflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), relative))
	require.NoError(t, err, "%s is missing", relative)
	var parsed workflow
	require.NoError(t, yaml.Unmarshal(raw, &parsed), relative)
	return parsed
}

// fencedBlocks returns the contents of every fenced code block in a Markdown
// document whose info string starts with one of the given languages.
func fencedBlocks(t *testing.T, relative, language string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), relative))
	require.NoError(t, err, "%s is missing", relative)

	var blocks []string
	var current *strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case current != nil && strings.HasPrefix(trimmed, "```"):
			blocks = append(blocks, current.String())
			current = nil
		case current != nil:
			current.WriteString(line + "\n")
		case strings.HasPrefix(trimmed, "```"+language):
			current = &strings.Builder{}
		}
	}
	require.Nil(t, current, "%s has an unterminated code fence", relative)
	return blocks
}

// The reusable workflow must have exactly one job, because the context name
// this repository publishes names one. A second job would report a second
// context that nothing requires — a gate that can fail without blocking a
// merge, which is the whole defect this gate was written against.
func TestTheDriftGateHasExactlyOneRequirableJob(t *testing.T) {
	parsed := readWorkflowFile(t, corePinDriftWorkflow)

	require.Len(t, parsed.Jobs, 1,
		"%s must declare exactly one job: the context name published in %s names "+
			"one, and any other job reports a context nothing requires",
		corePinDriftWorkflow, corePinDriftDoc)

	for id, job := range parsed.Jobs {
		require.Empty(t, job.Name,
			"the job's id is what the context name is built from, so it carries no "+
				"`name:` — a `name:` would silently become the published context's "+
				"second half")
		require.Empty(t, job.If,
			"a job-level `if:` can skip the job, and a skipped required check never "+
				"reports: every merge in nine repositories would wait on it forever")
		names, ok := checkNames(id, job)
		require.True(t, ok, "the job's check name must be knowable exactly")
		require.Len(t, names, 1)
	}
}

// It must be callable, and only callable: a `pull_request` trigger here would
// run core's own tree through a gate about consumers' pins, and core does not
// pin core.
func TestTheDriftGateIsReusableOnly(t *testing.T) {
	parsed := readWorkflowFile(t, corePinDriftWorkflow)

	triggerNames := triggers(parsed.On)
	require.Contains(t, triggerNames, "workflow_call",
		"%s is consumed by nine repositories through `uses:`", corePinDriftWorkflow)
	require.Len(t, triggerNames, 1,
		"workflow_call and nothing else; core does not pin core, so this gate has "+
			"nothing to say about core's own tree")

	// And therefore it must NOT join the set required on core's own main.
	// branchprotection_test.go derives that set and excludes workflow_call-only
	// workflows; this states the consequence so a later trigger addition fails
	// here with the reason rather than there with a list diff.
	require.False(t, alwaysRunsOnPullRequestsToMain(t, corePinDriftWorkflow, parsed),
		"adding a pull-request trigger here puts this job in the set "+
			"branchprotection_test.go requires on core's main, where it would fail "+
			"every pull request: core has no core pin to check")
}

// The snippet a consumer pastes must produce the context an administrator is
// told to require. This is the actual contract.
func TestTheDocumentedSnippetProducesTheDocumentedContext(t *testing.T) {
	parsed := readWorkflowFile(t, corePinDriftWorkflow)
	var reusableJobID string
	for id := range parsed.Jobs {
		reusableJobID = id
	}

	// The caller's half: find the snippet that calls this workflow.
	callerJobID := ""
	for _, block := range fencedBlocks(t, corePinDriftDoc, "yaml") {
		if !strings.Contains(block, "core-pin-drift.yml@") {
			continue
		}
		var snippet workflow
		require.NoError(t, yaml.Unmarshal([]byte(block), &snippet),
			"%s ships a snippet that is not valid YAML, so pasting it fails", corePinDriftDoc)
		require.Len(t, snippet.Jobs, 1,
			"%s's snippet must declare exactly one job, or the context it produces "+
				"is not the one documented", corePinDriftDoc)
		for id, job := range snippet.Jobs {
			callerJobID = id
			require.Empty(t, job.Name,
				"the snippet's job carries no `name:`: GitHub would use it as the "+
					"context's first half instead of the id")
		}
		// The snippet must reference THIS repository's workflow by path, or it
		// is documenting somebody else's gate.
		require.Contains(t, block, "codefly-dev/core/"+corePinDriftWorkflow+"@",
			"%s's snippet must call %s", corePinDriftDoc, corePinDriftWorkflow)
	}
	require.NotEmpty(t, callerJobID,
		"%s no longer contains a yaml snippet calling %s, so the one line a "+
			"consumer pastes is not in the document that tells them to paste it",
		corePinDriftDoc, corePinDriftWorkflow)

	want := callerJobID + " / " + reusableJobID

	// The prose must name exactly that, in a code fence of its own so it is
	// copy-pasteable rather than buried in a sentence.
	contexts := fencedBlocks(t, corePinDriftDoc, "")
	found := false
	for _, block := range contexts {
		if strings.TrimSpace(block) == want {
			found = true
		}
	}
	require.Truef(t, found,
		"%s must publish the required context name %q in a fenced block of its own.\n\n"+
			"GitHub builds it as `<caller job id> / <reusable job id>`, which here is "+
			"%q / %q. An administrator types this string into branch protection in nine "+
			"repositories; if the document says something else, each of them ends up "+
			"requiring a context that never reports, and every merge there waits on it.",
		corePinDriftDoc, want, callerJobID, reusableJobID)
}

// The document must keep stating the thresholds the checker enforces. The
// numbers are the whole decision codefly-dev/core#891 asked for, and a
// document disagreeing with the code is worse than no document: a reader
// plans a bump cadence against a window that is not the one they get.
func TestTheDocumentedThresholdsAreTheEnforcedOnes(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "corepindrift", "main.go"))
	require.NoError(t, err)

	constant := regexp.MustCompile(`(?m)^\s*(warnAfter|failAfter)\s*=\s*(\d+) \* 24 \* time\.Hour`)
	matches := constant.FindAllStringSubmatch(string(source), -1)
	require.Len(t, matches, 2,
		"internal/corepindrift/main.go no longer declares warnAfter and failAfter as "+
			"a plain number of days, so this guard cannot read them")

	days := map[string]string{}
	for _, match := range matches {
		days[match[1]] = match[2]
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), corePinDriftDoc))
	require.NoError(t, err)
	doc := string(raw)

	require.Contains(t, doc, "**"+days["warnAfter"]+" days**",
		"%s must state the warning threshold the code enforces (%s days)",
		corePinDriftDoc, days["warnAfter"])
	require.Contains(t, doc, "**"+days["failAfter"]+" days**",
		"%s must state the failure threshold the code enforces (%s days)",
		corePinDriftDoc, days["failAfter"])

	// And the count axis must stay documented as a non-threshold. If somebody
	// adds one, this fails and they have to argue for it in the document --
	// where the measurement against core's release bursts lives.
	require.NotContains(t, string(source), "failBehindCount",
		"a threshold on the NUMBER of versions behind is back. %s records why there "+
			"is none: core published 63 versions in its densest ten-day window, so any "+
			"count low enough to mean something fires on a repository three days "+
			"behind. Make the case in the document before making it in the code.",
		corePinDriftDoc)
}
