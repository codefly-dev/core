package services

import (
	"context"
	"errors"
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

	// A restart that does not say what it ran must not carry the previous
	// answer forward, and saying nothing is simply not calling again — the
	// path an agent takes when its resolution is skipped or fails. The build
	// then goes unreported and the consumer refuses, rather than approving
	// the process before this one for the process running now.
	third, _ := wrapper.StartResponse()
	if _, reported = StartedArtifact(third.GetStatus()); reported {
		t.Fatalf("a restart that reported nothing carried the previous process's artifact: %+v", third.GetStatus().GetStartedArtifact())
	}
	// What the earlier start reported is still the earlier start's answer:
	// clearing is about the next process, not about rewriting the status
	// already handed out for the one before it.
	if second.GetStatus().GetStartedArtifact().GetExecutable() != "/opt/venv/bin/python" {
		t.Fatal("clearing rewrote the status of the start that did report an artifact")
	}

	// A start that failed takes its report with it. The agent named an
	// artifact, the process never came up, and the retry must not have that
	// artifact approved for the process it does bring up.
	wrapper.ReportStartedArtifact(&runtimev0.StartedArtifact{Executable: "/opt/venv-old/bin/python"})
	if _, err = wrapper.StartError(errors.New("port already in use")); err != nil {
		t.Fatalf("StartError: %v", err)
	}
	retry, _ := wrapper.StartResponse()
	if _, reported = StartedArtifact(retry.GetStatus()); reported {
		t.Fatalf("a report made for an attempt that never started was stamped on the start that did: %+v", retry.GetStatus().GetStartedArtifact())
	}

	// An explicit nil withdraws a report that has not been stamped yet.
	wrapper.ReportStartedArtifact(&runtimev0.StartedArtifact{Executable: "/opt/venv/bin/python"})
	wrapper.ReportStartedArtifact(nil)
	fourth, _ := wrapper.StartResponse()
	if _, reported = StartedArtifact(fourth.GetStatus()); reported {
		t.Fatal("a withdrawn artifact was still reported")
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
