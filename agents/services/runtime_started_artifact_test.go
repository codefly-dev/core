package services

import (
	"context"
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// An agent says what it started; the status carries that answer to Start and
// to Information alike, so a consumer pinning the build a process runs never
// has to derive a path from the agent's layout.
func TestAStartedArtifactIsReportedByTheAgentAndSurvivesToInformation(t *testing.T) {
	wrapper := &RuntimeWrapper{}

	// An agent that never reports one reports nothing: not an empty artifact
	// a consumer could mistake for an answer.
	first, _ := wrapper.StartResponse()
	if first.GetStatus().GetStartedArtifact() != nil {
		t.Fatalf("an agent that reported no artifact carried one: %+v", first.GetStatus().GetStartedArtifact())
	}
	if _, reported := StartedArtifact(first.GetStatus()); reported {
		t.Fatal("StartedArtifact claimed an artifact the agent never reported")
	}

	// An interpreted runtime starts an interpreter and names the sources it
	// was handed beside it.
	submitted := &runtimev0.StartedArtifact{
		Executable: "/opt/venv/bin/python",
		Sources:    []string{"/workspace/services/model/src/main.py"},
	}
	wrapper.ReportStartedArtifact(submitted)
	second, _ := wrapper.StartResponse()
	artifact, reported := StartedArtifact(second.GetStatus())
	if !reported || artifact.GetExecutable() != "/opt/venv/bin/python" || len(artifact.GetSources()) != 1 {
		t.Fatalf("start status carried %+v, want the reported artifact", second.GetStatus().GetStartedArtifact())
	}

	// The agent keeps its own message: a later stamp must not reach into it.
	submitted.Sources = append(submitted.Sources, "/workspace/services/model/src/late.py")
	information, err := wrapper.InformationResponse(context.Background(), &runtimev0.InformationRequest{})
	if err != nil {
		t.Fatalf("InformationResponse: %v", err)
	}
	if got := information.GetStartStatus().GetStartedArtifact().GetSources(); len(got) != 1 {
		t.Fatalf("information reported sources %v, want the one the agent reported at Start", got)
	}

	// A restart that cannot say what it ran must not carry the previous
	// answer forward: the build then goes unreported, and the consumer
	// refuses rather than approving the process before it.
	wrapper.ReportStartedArtifact(nil)
	third, _ := wrapper.StartResponse()
	if _, reported = StartedArtifact(third.GetStatus()); reported {
		t.Fatal("a cleared artifact was still reported after a restart")
	}
}

// An artifact naming no program names nothing, however many sources it lists:
// the consumer must land in the same refusal as an agent that reported none.
func TestAnArtifactWithNoExecutableIsNoArtifact(t *testing.T) {
	status := &runtimev0.StartStatus{
		State:           runtimev0.StartStatus_STARTED,
		StartedArtifact: &runtimev0.StartedArtifact{Sources: []string{"/workspace/services/model/src/main.py"}},
	}
	if _, reported := StartedArtifact(status); reported {
		t.Fatal("an artifact naming no executable was reported as an artifact")
	}
	if _, reported := StartedArtifact(nil); reported {
		t.Fatal("StartedArtifact(nil) must not claim a reported artifact")
	}
}
