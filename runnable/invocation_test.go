package runnable_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
)

var issued = time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

func sampleInvocation(pkg *basev0.RunnablePackage) *basev0.RunnableInvocation {
	return &basev0.RunnableInvocation{
		Protocol:     runnable.ProtocolV1,
		Runnable:     proto.Clone(pkg.GetIdentity()).(*basev0.RunnableIdentity),
		InvocationId: "inv-1",
		IntentId:     "intent-1",
		IssuedAt:     timestamppb.New(issued),
		Deadline:     timestamppb.New(issued.Add(2 * time.Minute)),
		Input:        []byte(`{"text":"the quick brown fox"}`),
	}
}

func resultDocument(t *testing.T, result *basev0.RunnableResult) []byte {
	t.Helper()
	document, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(result)
	require.NoError(t, err)
	return document
}

func succeededDocument(t *testing.T) []byte {
	t.Helper()
	return resultDocument(t, &basev0.RunnableResult{
		Protocol:     runnable.ProtocolV1,
		InvocationId: "inv-1",
		Status:       basev0.RunnableResult_SUCCEEDED,
		Output:       []byte(`{"count":4,"longest":"quick"}`),
	})
}

func observed(document []byte) runnable.Observation {
	return runnable.Observation{
		StartedAt: issued,
		EndedAt:   issued.Add(time.Second),
		Result:    document,
		Stdout:    runnable.LogStream{Bytes: 12},
		Stderr:    runnable.LogStream{Bytes: 4096, Truncated: true},
	}
}

func TestInvocationDocumentIsTheWholeSeam(t *testing.T) {
	pkg := preparedPackage(t)
	invocation, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	document, err := runnable.EncodeInvocation(invocation)
	require.NoError(t, err)

	// A harness reads proto field names, not Go or JSON camelCase spellings,
	// and decodes the payload from the proto3 JSON bytes mapping.
	var generic map[string]any
	require.NoError(t, json.Unmarshal(document, &generic))
	require.Contains(t, generic, "invocation_id")
	require.Contains(t, generic, "issued_at")

	decoded := &basev0.RunnableInvocation{}
	require.NoError(t, protojson.Unmarshal(document, decoded))
	require.True(t, proto.Equal(invocation, decoded))
	require.JSONEq(t, `{"text":"the quick brown fox"}`, string(decoded.GetInput()))

	require.Equal(t, map[string]string{
		"CODEFLY__RUNNABLE_PROTOCOL":   runnable.ProtocolV1,
		"CODEFLY__RUNNABLE_INVOCATION": "/tmp/inv-1/invocation.json",
		"CODEFLY__RUNNABLE_RESULT":     "/tmp/inv-1/result.json",
	}, runnable.InvocationEnvironment(invocation, "/tmp/inv-1/invocation.json", "/tmp/inv-1/result.json"))
}

func TestPrepareInvocationRejectsWhatALauncherCannotRun(t *testing.T) {
	pkg := preparedPackage(t)
	for _, tc := range []struct {
		name   string
		mutate func(*basev0.RunnableInvocation)
		want   string
	}{
		{"other protocol", func(i *basev0.RunnableInvocation) { i.Protocol = "codefly.runnable/v2" }, "is not the package's"},
		{"other release", func(i *basev0.RunnableInvocation) { i.Runnable.Version = "0.2.0" }, "names another release"},
		{"no invocation id", func(i *basev0.RunnableInvocation) { i.InvocationId = "" }, "invocation_id"},
		{"no intent id", func(i *basev0.RunnableInvocation) { i.IntentId = "" }, "intent_id"},
		{"no deadline", func(i *basev0.RunnableInvocation) { i.Deadline = nil }, "deadline"},
		{"deadline before issue", func(i *basev0.RunnableInvocation) { i.Deadline = timestamppb.New(issued.Add(-time.Second)) }, "not after the instant it was issued"},
		{"no input", func(i *basev0.RunnableInvocation) { i.Input = nil }, "input"},
		{"input over bound", func(i *basev0.RunnableInvocation) {
			i.Input = []byte(`{"text":"` + strings.Repeat("x", 65536) + `"}`)
		}, "over the declared bound"},
		{"input is not an object", func(i *basev0.RunnableInvocation) { i.Input = []byte(`["text"]`) }, "must be one JSON object"},
		{"input is not JSON", func(i *basev0.RunnableInvocation) { i.Input = []byte(`{text}`) }, "must be one JSON object"},
		{"effect id without an effect", func(i *basev0.RunnableInvocation) { i.EffectId = "effect-1" }, "must carry no effect identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invocation := sampleInvocation(pkg)
			tc.mutate(invocation)
			_, err := runnable.PrepareInvocation(invocation, pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}
	_, err := runnable.PrepareInvocation(nil, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestReceiptRecoveryRequiresTheEffectIdentity(t *testing.T) {
	effectful := samplePackage(t)
	effectful.Execution.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
	pkg, err := runnable.PreparePackage(effectful)
	require.NoError(t, err)

	_, err = runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "must carry the effect identity")

	invocation := sampleInvocation(pkg)
	invocation.EffectId = "effect-1"
	_, err = runnable.PrepareInvocation(invocation, pkg)
	require.NoError(t, err)
}

func TestParseResultAcceptsOnlyThisInvocationsCompletion(t *testing.T) {
	pkg := preparedPackage(t)
	invocation, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	result, err := runnable.ParseResult(succeededDocument(t), invocation, pkg)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableResult_SUCCEEDED, result.GetStatus())

	failed := resultDocument(t, &basev0.RunnableResult{
		Protocol:     runnable.ProtocolV1,
		InvocationId: "inv-1",
		Status:       basev0.RunnableResult_FAILED,
		Error:        &basev0.RunnableError{Code: "empty_text", Message: "text has no words"},
	})
	result, err = runnable.ParseResult(failed, invocation, pkg)
	require.NoError(t, err)
	require.Equal(t, "empty_text", result.GetError().GetCode())

	for _, tc := range []struct {
		name     string
		document []byte
		want     string
	}{
		{"malformed", []byte("not json at all"), "is not a codefly.runnable/v1 document"},
		{"another invocation", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-0", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`),
		}), "belongs to invocation \"inv-0\""},
		{"another protocol", resultDocument(t, &basev0.RunnableResult{
			Protocol: "codefly.runnable/v2", InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`),
		}), "is not the invocation's"},
		{"no status", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Output: []byte(`{"count":4}`),
		}), "is not a completion a harness can report"},
		{"no output", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
		}), "output must be one JSON object"},
		{"output over bound", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":"` + strings.Repeat("x", int(pkg.GetExecution().GetMaxOutputBytes())) + `"}`),
		}), "over the declared bound"},
		{"succeeded with an error", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`), Error: &basev0.RunnableError{Code: "nope"},
		}), "succeeded result carries no error"},
		{"failed with output", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_FAILED,
			Output: []byte(`{"count":4}`), Error: &basev0.RunnableError{Code: "nope"},
		}), "failed result carries no output"},
		{"failed without a code", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_FAILED,
		}), "requires the handler's failure code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runnable.ParseResult(tc.document, invocation, pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCompleteClassifiesEveryWayAnInvocationEnds(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)

	invalid := resultDocument(t, &basev0.RunnableResult{
		Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
	})
	failed := resultDocument(t, &basev0.RunnableResult{
		Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_FAILED,
		Error: &basev0.RunnableError{Code: "empty_text"},
	})

	for _, tc := range []struct {
		name    string
		observe func(*runnable.Observation)
		want    basev0.RunnableCompletion_Outcome
	}{
		{"typed output", func(o *runnable.Observation) { o.Result = succeededDocument(t) }, basev0.RunnableCompletion_SUCCEEDED},
		{"handler failure", func(o *runnable.Observation) { o.Result = failed }, basev0.RunnableCompletion_FAILED},
		{"exit zero with invalid output", func(o *runnable.Observation) { o.Result = invalid }, basev0.RunnableCompletion_INVALID_OUTPUT},
		{"exit zero with no output", func(o *runnable.Observation) {}, basev0.RunnableCompletion_MISSING_OUTPUT},
		{"non-zero exit", func(o *runnable.Observation) { o.ExitCode = 2 }, basev0.RunnableCompletion_CRASHED},
		{"killed by a signal", func(o *runnable.Observation) { o.Signal = "SIGKILL" }, basev0.RunnableCompletion_CRASHED},
		{"deadline", func(o *runnable.Observation) {
			o.Ended = runnable.EndedOnDeadline
			o.Signal = "SIGKILL"
		}, basev0.RunnableCompletion_TIMED_OUT},
		{"cancellation", func(o *runnable.Observation) {
			o.Ended = runnable.EndedOnCancel
			o.Signal = "SIGINT"
		}, basev0.RunnableCompletion_CANCELED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := observed(nil)
			tc.observe(&observation)
			completion, err := runnable.Complete(invocation, pkg, observation)
			require.NoError(t, err)
			require.Equal(t, tc.want, completion.GetOutcome())
			require.Equal(t, "inv-1", completion.GetInvocationId())
			require.True(t, proto.Equal(pkg.GetIdentity(), completion.GetRunnable()))

			// A result travels with exactly the two outcomes it proves, and a
			// message explains the outcomes that carry none.
			if runnable.OutcomeIsCertain(tc.want) {
				require.NotNil(t, completion.GetResult())
				require.Empty(t, completion.GetMessage())
			} else {
				require.Nil(t, completion.GetResult())
				require.NotEmpty(t, completion.GetMessage())
			}

			// Logs are metadata: bounded counts and a truncation flag, never
			// the process output itself.
			require.Equal(t, uint64(12), completion.GetLogs().GetStdout().GetBytes())
			require.True(t, completion.GetLogs().GetStderr().GetTruncated())
		})
	}
}

func TestCompletePrefersAProvenOutcomeOverHowTheLauncherEndedIt(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)

	// The harness renamed a valid result into place before the launcher's kill
	// landed: discarding it would force a recovery for an outcome already
	// proven.
	observation := observed(succeededDocument(t))
	observation.Ended = runnable.EndedOnDeadline
	observation.Signal = "SIGKILL"
	completion, err := runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_SUCCEEDED, completion.GetOutcome())
	require.Equal(t, "SIGKILL", completion.GetSignal())

	// An invalid document from a process the launcher ended is explained by
	// the launcher's own action, not blamed on the harness.
	observation = observed([]byte("half a document"))
	observation.Ended = runnable.EndedOnDeadline
	completion, err = runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_TIMED_OUT, completion.GetOutcome())
}

func TestCompleteRefusesToInterruptAnUninterruptiblePackage(t *testing.T) {
	uninterruptible := samplePackage(t)
	uninterruptible.Execution.Cancellation = basev0.RunnableExecution_CANCELLATION_NONE
	pkg, err := runnable.PreparePackage(uninterruptible)
	require.NoError(t, err)

	observation := observed(nil)
	observation.Ended = runnable.EndedOnCancel
	_, err = runnable.Complete(sampleInvocation(pkg), pkg, observation)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "must not interrupt it")
}

func TestOutcomeIsCertainOnlyWhenTheHarnessReported(t *testing.T) {
	for outcome, name := range basev0.RunnableCompletion_Outcome_name {
		certain := runnable.OutcomeIsCertain(basev0.RunnableCompletion_Outcome(outcome))
		require.Equal(t, name == "SUCCEEDED" || name == "FAILED", certain, name)
	}
}
