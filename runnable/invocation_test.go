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
		StartedAt:     issued,
		EndedAt:       issued.Add(time.Second),
		ResultPresent: document != nil,
		Result:        document,
		Stdout:        runnable.LogStream{Bytes: 12},
		Stderr:        runnable.LogStream{Bytes: 4096, Truncated: true},
	}
}

func withResult(o *runnable.Observation, document []byte) {
	o.ResultPresent, o.Result = true, document
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
		{"deadline beyond the declared timeout", func(i *basev0.RunnableInvocation) {
			i.Deadline = timestamppb.New(issued.Add(2*time.Minute + time.Second))
		}, "the package declares a timeout of"},
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

// The declared timeout bounds one invocation's duration, so a launcher may
// shorten a deadline but never overrule the author with a longer one.
func TestDeadlineHonorsTheDeclaredTimeout(t *testing.T) {
	pkg := preparedPackage(t)
	require.Equal(t, 2*time.Minute, pkg.GetExecution().GetTimeout().AsDuration())

	exact := sampleInvocation(pkg)
	exact.Deadline = timestamppb.New(issued.Add(2 * time.Minute))
	_, err := runnable.PrepareInvocation(exact, pkg)
	require.NoError(t, err)

	shorter := sampleInvocation(pkg)
	shorter.Deadline = timestamppb.New(issued.Add(30 * time.Second))
	_, err = runnable.PrepareInvocation(shorter, pkg)
	require.NoError(t, err)

	longer := sampleInvocation(pkg)
	longer.Deadline = timestamppb.New(issued.Add(10 * time.Hour))
	_, err = runnable.PrepareInvocation(longer, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "invocation allows 10h0m0s but the package declares a timeout of 2m0s")
}

// A caller with one identity scheme for all its work carries an effect
// identity everywhere; only a receipt-recovery package requires one.
func TestEffectIdentityIsCarriedOnRecomputePackages(t *testing.T) {
	pkg := preparedPackage(t)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, pkg.GetExecution().GetRecovery())

	invocation := sampleInvocation(pkg)
	invocation.EffectId = "task-1"
	prepared, err := runnable.PrepareInvocation(invocation, pkg)
	require.NoError(t, err)
	require.Equal(t, "task-1", prepared.GetEffectId())
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
		{"typed output", func(o *runnable.Observation) { withResult(o, succeededDocument(t)) }, basev0.RunnableCompletion_SUCCEEDED},
		{"handler failure", func(o *runnable.Observation) { withResult(o, failed) }, basev0.RunnableCompletion_FAILED},
		{"exit zero with invalid output", func(o *runnable.Observation) { withResult(o, invalid) }, basev0.RunnableCompletion_INVALID_OUTPUT},
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
	observation = observed(nil)
	withResult(&observation, []byte("half a document"))
	observation.Ended = runnable.EndedOnDeadline
	completion, err = runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_TIMED_OUT, completion.GetOutcome())
}

func TestCompleteRecordsAForbiddenCancellationWithoutDiscardingTheOutcome(t *testing.T) {
	uninterruptible := samplePackage(t)
	uninterruptible.Execution.Cancellation = basev0.RunnableExecution_CANCELLATION_NONE
	pkg, err := runnable.PreparePackage(uninterruptible)
	require.NoError(t, err)
	invocation := sampleInvocation(pkg)

	// The harness had already renamed a valid success into place when the
	// launcher's interrupt landed. Refusing to report it would lose a proven
	// outcome and send an effectful caller looking for a receipt it does not need.
	observation := observed(succeededDocument(t))
	observation.Ended = runnable.EndedOnCancel
	completion, err := runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_SUCCEEDED, completion.GetOutcome())
	require.True(t, runnable.OutcomeIsCertain(completion.GetOutcome()))
	require.Contains(t, completion.GetMessage(), "declares cancellation CANCELLATION_NONE")

	// With no result the invocation is still a fact, and the breach is on it.
	observation = observed(nil)
	observation.Ended = runnable.EndedOnCancel
	completion, err = runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_CANCELED, completion.GetOutcome())
	require.Contains(t, completion.GetMessage(), "declares cancellation CANCELLATION_NONE")
	require.Contains(t, completion.GetMessage(), "before the harness reported a result")
}

// A package promising CANCELLATION_SIGNAL promises the harness reports the
// interruption. Without a status for it the harness would have to claim a
// failure, which reads as proof the effect did not happen.
func TestInterruptedHarnessLeavesTheEffectUnproven(t *testing.T) {
	effectful := samplePackage(t)
	effectful.Execution.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
	pkg, err := runnable.PreparePackage(effectful)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_CANCELLATION_SIGNAL, pkg.GetExecution().GetCancellation())

	invocation := sampleInvocation(pkg)
	invocation.EffectId = "effect-1"

	interrupted := resultDocument(t, &basev0.RunnableResult{
		Protocol:     runnable.ProtocolV1,
		InvocationId: "inv-1",
		Status:       basev0.RunnableResult_INTERRUPTED,
		Error:        &basev0.RunnableError{Code: "interrupted", Message: "SIGINT during the charge"},
	})
	observation := observed(interrupted)
	observation.Ended = runnable.EndedOnCancel
	observation.Signal = "SIGINT"
	completion, err := runnable.Complete(invocation, pkg, observation)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_CANCELED, completion.GetOutcome())
	require.False(t, runnable.OutcomeIsCertain(completion.GetOutcome()),
		"an interrupted harness must not be read as proof the effect did not happen")
	require.Equal(t, basev0.RunnableResult_INTERRUPTED, completion.GetResult().GetStatus())

	// It is a report about a stopped handler, so it carries no output. The
	// explanatory error is optional.
	withOutput := &basev0.RunnableResult{
		Protocol: runnable.ProtocolV1, InvocationId: "inv-1",
		Status: basev0.RunnableResult_INTERRUPTED, Output: []byte(`{"count":4}`),
	}
	_, err = runnable.ParseResult(resultDocument(t, withOutput), invocation, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "an interrupted result carries no output")

	bare := &basev0.RunnableResult{
		Protocol: runnable.ProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_INTERRUPTED,
	}
	_, err = runnable.ParseResult(resultDocument(t, bare), invocation, pkg)
	require.NoError(t, err)
}

// A harness generated from newer sources emits a field this core does not
// know. Rejecting it would make every invocation of that runnable uncertain.
func TestParseResultToleratesANewerHarness(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)

	document := []byte(`{"protocol":"codefly.runnable/v1","invocation_id":"inv-1","status":"SUCCEEDED",` +
		`"output":"eyJjb3VudCI6NH0=","duration_ms":42}`)
	completion, err := runnable.Complete(invocation, pkg, observed(document))
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_SUCCEEDED, completion.GetOutcome())
	require.True(t, runnable.OutcomeIsCertain(completion.GetOutcome()))
}

func TestCompleteRejectsAnObservationItCannotRecord(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)

	for _, tc := range []struct {
		name    string
		observe func(*runnable.Observation)
		want    string
	}{
		// A zero time satisfies the wire contract's "required" and would be
		// persisted as year one.
		{"no start", func(o *runnable.Observation) { o.StartedAt = time.Time{} }, "must bracket the process"},
		{"no end", func(o *runnable.Observation) { o.EndedAt = time.Time{} }, "must bracket the process"},
		{"ends before it starts", func(o *runnable.Observation) { o.EndedAt = o.StartedAt.Add(-time.Hour) }, "before it starts"},
		{"result without presence", func(o *runnable.Observation) {
			o.ResultPresent, o.Result = false, succeededDocument(t)
		}, "does not report one as present"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := observed(nil)
			tc.observe(&observation)
			_, err := runnable.Complete(invocation, pkg, observation)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}

	// An empty file at the result path is a document, not a missing one.
	empty := observed(nil)
	withResult(&empty, []byte{})
	completion, err := runnable.Complete(invocation, pkg, empty)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableCompletion_INVALID_OUTPUT, completion.GetOutcome())
}

func TestOutcomeIsCertainOnlyWhenTheHarnessReported(t *testing.T) {
	for outcome, name := range basev0.RunnableCompletion_Outcome_name {
		certain := runnable.OutcomeIsCertain(basev0.RunnableCompletion_Outcome(outcome))
		require.Equal(t, name == "SUCCEEDED" || name == "FAILED", certain, name)
	}
}
