package runnable_test

import (
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
		Protocol:     runnable.ServedProtocolV1,
		Runnable:     proto.Clone(pkg.GetIdentity()).(*basev0.RunnableIdentity),
		InvocationId: "inv-1",
		IntentId:     "intent-1",
		IssuedAt:     timestamppb.New(issued),
		Deadline:     timestamppb.New(issued.Add(2 * time.Minute)),
		Input:        []byte(`{"text":"the quick brown fox"}`),
		Identity: &basev0.RunnableInvocationIdentity{
			Carrier: &basev0.RunnableInvocationIdentity_WorkContext{WorkContext: "eyJ0eXAiOiJjb2RlZmx5LndvcmstY29udGV4dC92MSJ9.signed"},
		},
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
		Protocol:     runnable.ServedProtocolV1,
		InvocationId: "inv-1",
		Status:       basev0.RunnableResult_SUCCEEDED,
		Output:       []byte(`{"count":4,"longest":"quick"}`),
	})
}

func TestPrepareInvocationRejectsWhatCannotBeCalled(t *testing.T) {
	pkg := preparedPackage(t)
	for _, tc := range []struct {
		name   string
		mutate func(*basev0.RunnableInvocation)
		want   string
	}{
		{"other protocol", func(i *basev0.RunnableInvocation) { i.Protocol = "codefly.runnable.served/v2" }, "is not the package's"},
		// The framing this replaces. A package built for the served protocol
		// and an invocation still written in the launcher's is the one
		// combination the transition could leave behind, and it is refused by
		// name rather than half-read.
		{"the deleted launcher framing", func(i *basev0.RunnableInvocation) { i.Protocol = "codefly.runnable/v1" }, "is not the package's"},
		{"other release", func(i *basev0.RunnableInvocation) { i.Runnable.Version = "0.2.0" }, "names another release"},
		{"no invocation id", func(i *basev0.RunnableInvocation) { i.InvocationId = "" }, "invocation_id"},
		{"no intent id", func(i *basev0.RunnableInvocation) { i.IntentId = "" }, "intent_id"},
		{"no deadline", func(i *basev0.RunnableInvocation) { i.Deadline = nil }, "deadline"},
		{"deadline before issue", func(i *basev0.RunnableInvocation) { i.Deadline = timestamppb.New(issued.Add(-time.Second)) }, "not after the instant it was issued"},
		{"no identity", func(i *basev0.RunnableInvocation) { i.Identity = nil }, "identity"},
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

// The declared timeout bounds one invocation's duration, so a caller may
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

func TestParseResultAcceptsOnlyThisInvocationsAnswer(t *testing.T) {
	pkg := preparedPackage(t)
	invocation, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	result, err := runnable.ParseResult(succeededDocument(t), invocation, pkg)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableResult_SUCCEEDED, result.GetStatus())

	failed := resultDocument(t, &basev0.RunnableResult{
		Protocol:     runnable.ServedProtocolV1,
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
		{"malformed", []byte("not json at all"), "is not a codefly.runnable.served/v1 document"},
		{"another invocation", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-0", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`),
		}), "belongs to invocation \"inv-0\""},
		{"another protocol", resultDocument(t, &basev0.RunnableResult{
			Protocol: "codefly.runnable.served/v2", InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`),
		}), "is not the invocation's"},
		{"no status", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Output: []byte(`{"count":4}`),
		}), "is not a completion an implementation can report"},
		{"no output", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
		}), "output must be one JSON object"},
		{"output over bound", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":"` + strings.Repeat("x", int(pkg.GetExecution().GetMaxOutputBytes())) + `"}`),
		}), "over the declared bound"},
		{"succeeded with an error", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_SUCCEEDED,
			Output: []byte(`{"count":4}`), Error: &basev0.RunnableError{Code: "nope"},
		}), "succeeded result carries no error"},
		{"failed with output", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_FAILED,
			Output: []byte(`{"count":4}`), Error: &basev0.RunnableError{Code: "nope"},
		}), "failed result carries no output"},
		{"failed without a code", resultDocument(t, &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_FAILED,
		}), "requires the handler's failure code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runnable.ParseResult(tc.document, invocation, pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// A package promising CANCELLATION_SIGNAL promises the implementation reports
// the interruption. Without a status for it the implementation would have to
// claim a failure, which reads as proof the effect did not happen.
func TestInterruptedInvocationCarriesNoOutput(t *testing.T) {
	effectful := samplePackage(t)
	effectful.Execution.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
	pkg, err := runnable.PreparePackage(effectful)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_CANCELLATION_SIGNAL, pkg.GetExecution().GetCancellation())

	invocation := sampleInvocation(pkg)
	invocation.EffectId = "effect-1"

	// It is a report about a stopped handler, so it carries no output. The
	// explanatory error is optional.
	withOutput := &basev0.RunnableResult{
		Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1",
		Status: basev0.RunnableResult_INTERRUPTED, Output: []byte(`{"count":4}`),
	}
	_, err = runnable.ParseResult(resultDocument(t, withOutput), invocation, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "an interrupted result carries no output")

	bare := &basev0.RunnableResult{
		Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1", Status: basev0.RunnableResult_INTERRUPTED,
	}
	_, err = runnable.ParseResult(resultDocument(t, bare), invocation, pkg)
	require.NoError(t, err)
}

// An implementation generated from newer sources emits a field this core does
// not know. Rejecting it would make every invocation of that runnable uncertain.
func TestParseResultToleratesANewerImplementation(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)

	document := []byte(`{"protocol":"codefly.runnable.served/v1","invocation_id":"inv-1","status":"SUCCEEDED",` +
		`"output":"eyJjb3VudCI6NH0=","duration_ms":42}`)
	result, err := runnable.ParseResult(document, invocation, pkg)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableResult_SUCCEEDED, result.GetStatus())
}

// An empty answer is a document, not a missing one: a caller that read zero
// bytes has something that is not this invocation's result, which is an
// integrity failure rather than a call that never completed.
func TestAnEmptyAnswerIsNotAMissingOne(t *testing.T) {
	pkg := preparedPackage(t)
	invocation := sampleInvocation(pkg)
	_, err := runnable.ParseResult([]byte{}, invocation, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}
