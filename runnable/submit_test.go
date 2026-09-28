package runnable_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

// submittedPackage is the sample package declaring the mode whose answer
// arrives later. RECOVERY_RECEIPT comes with it rather than being a choice: a
// terminal answer the caller may never receive has to be resolvable by reading
// the effect, and PreparePackage refuses the pair without it.
func submittedPackage(t *testing.T) *basev0.RunnablePackage {
	t.Helper()
	declared := samplePackage(t)
	declared.Execution.Completion = basev0.RunnableExecution_COMPLETION_SUBMIT
	declared.Execution.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
	pkg, err := runnable.PreparePackage(declared)
	require.NoError(t, err)
	return pkg
}

func submittedInvocation(t *testing.T, pkg *basev0.RunnablePackage) *basev0.RunnableInvocation {
	t.Helper()
	inv := sampleInvocation(pkg)
	inv.EffectId = "effect-1"
	inv.Callback = &basev0.RunnableCallbackTarget{Address: "https://runtime.example/codefly/runnable/reports", Audience: "runtime"}
	prepared, err := runnable.PrepareInvocation(inv, pkg)
	require.NoError(t, err)
	return prepared
}

func acceptance() *runnablev0.RunnableAcceptance {
	return &runnablev0.RunnableAcceptance{
		Schema:            runnable.AcceptanceSchemaV1,
		InvocationId:      "inv-1",
		Handle:            "owner-work-7f3c",
		AcceptedAt:        timestamppb.New(issued.Add(time.Second)),
		HeartbeatInterval: durationpb.New(30 * time.Second),
	}
}

func submitDocument(t *testing.T, message proto.Message) []byte {
	t.Helper()
	document, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	require.NoError(t, err)
	return document
}

// The acceptance is the reply to the submit call, and it proves acceptance and
// nothing else. A caller that read it as a success would stop waiting for the
// answer that proves something, so no outcome in the taxonomy means "submitted".
func TestAnAcceptanceProvesAcceptanceAndNothingAboutTheEffect(t *testing.T) {
	pkg := submittedPackage(t)
	inv := submittedInvocation(t, pkg)

	observed := servedCall()
	observed.Response = submitDocument(t, acceptance())
	accepted, err := runnable.ClassifySubmit(inv, pkg, observed)
	require.NoError(t, err)
	require.Nil(t, accepted.Completion, "an accepted submission has not completed")
	require.Equal(t, "owner-work-7f3c", accepted.Acceptance.GetHandle())
	require.Equal(t, 30*time.Second, accepted.Acceptance.GetHeartbeatInterval().AsDuration())
}

// Which shape a reply has follows from the declared completion mode, so reading
// an acceptance out of a call-mode package's reply is refused rather than
// attempted: an owner's own output document carrying a "handle" field would
// otherwise be read as an acceptance of work nobody submitted.
func TestOnlyASubmitPackagesReplyIsReadAsAnAcceptance(t *testing.T) {
	pkg := preparedPackage(t)
	inv, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	observed := servedCall()
	observed.Response = submitDocument(t, acceptance())
	_, err = runnable.ClassifySubmit(inv, pkg, observed)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "COMPLETION_CALL")
}

// Every way a submit call can end other than acceptance is the served taxonomy
// unchanged: the reply proves less, and how the call itself ended proves exactly
// what it proved before.
func TestASubmitCallThatDidNotAcceptIsTheServedTaxonomy(t *testing.T) {
	pkg := submittedPackage(t)
	inv := submittedInvocation(t, pkg)

	for _, tc := range []struct {
		name    string
		call    func(runnable.Call) runnable.Call
		outcome basev0.RunnableServedOutcome
		certain bool
	}{
		{
			name: "the owner's own typed code refuses the submission",
			call: func(c runnable.Call) runnable.Call {
				c.FailureCode = "quota_exhausted"
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_OWNER_FAILED,
			certain: true,
		},
		{
			name: "nothing was sent",
			call: func(c runnable.Call) runnable.Call {
				c.AuthorityResolved = false
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_AUTHORITY_UNAVAILABLE,
			certain: true,
		},
		{
			name: "the call never completed, so the submission may have been accepted",
			call: func(c runnable.Call) runnable.Call {
				c.Answered = false
				c.Trouble = errors.New("connection reset")
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE,
			certain: false,
		},
		{
			name: "an answer that is not an acceptance",
			call: func(c runnable.Call) runnable.Call {
				c.Response = []byte(`{"schema":"codefly.runnable-acceptance/v1","invocation_id":"inv-1"}`)
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY,
			certain: false,
		},
		{
			name: "an acceptance of another invocation",
			call: func(c runnable.Call) runnable.Call {
				other := acceptance()
				other.InvocationId = "inv-2"
				c.Response = submitDocument(t, other)
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY,
			certain: false,
		},
		{
			name: "an acceptance undertaking no heartbeat interval",
			call: func(c runnable.Call) runnable.Call {
				silent := acceptance()
				silent.HeartbeatInterval = durationpb.New(0)
				c.Response = submitDocument(t, silent)
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY,
			certain: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted, err := runnable.ClassifySubmit(inv, pkg, tc.call(servedCall()))
			require.NoError(t, err)
			require.Nil(t, accepted.Acceptance)
			require.Equal(t, tc.outcome, accepted.Completion.GetOutcome())
			require.Equal(t, tc.certain, runnable.ServedOutcomeIsCertain(accepted.Completion.GetOutcome()))
			require.NotEqual(t, basev0.RunnableServedOutcome_SERVED_SUCCEEDED, accepted.Completion.GetOutcome(),
				"a submit reply is not the work's answer, so no way of ending it can prove the effect committed")
		})
	}
}

// The callback address receives two shapes, so the schema decides which
// arrived. It is the one place in this contract where a document's shape is
// read off the document.
func TestTheCallbackAddressIsSortedByTheDocumentsOwnSchema(t *testing.T) {
	pkg := submittedPackage(t)
	inv := submittedInvocation(t, pkg)
	accepted := acceptance()

	heartbeat := submitDocument(t, &runnablev0.RunnableHeartbeat{
		Schema:       runnable.HeartbeatSchemaV1,
		Handle:       accepted.GetHandle(),
		InvocationId: "inv-1",
		ObservedAt:   timestamppb.New(issued.Add(time.Minute)),
		Progress:     &runnablev0.RunnableProgress{Done: 3, Total: 10, Note: "reconciling"},
	})
	reported, err := runnable.ClassifyReport(heartbeat, inv, pkg, accepted)
	require.NoError(t, err)
	require.Nil(t, reported.Completion, "a heartbeat is never terminal")
	require.Equal(t, uint64(3), reported.Heartbeat.GetProgress().GetDone())

	callback := submitDocument(t, &runnablev0.RunnableCompletionCallback{
		Schema:       runnable.CallbackSchemaV1,
		Handle:       accepted.GetHandle(),
		InvocationId: "inv-1",
		EffectId:     "effect-1",
		Result: &basev0.RunnableResult{
			Protocol:     runnable.ServedProtocolV1,
			InvocationId: "inv-1",
			Status:       basev0.RunnableResult_SUCCEEDED,
			Output:       []byte(`{"count":4,"longest":"quick"}`),
		},
		CompletedAt: timestamppb.New(issued.Add(time.Hour)),
	})
	reported, err = runnable.ClassifyReport(callback, inv, pkg, accepted)
	require.NoError(t, err)
	require.Nil(t, reported.Heartbeat)
	require.Equal(t, basev0.RunnableServedOutcome_SERVED_SUCCEEDED, reported.Completion.GetOutcome())
	require.True(t, runnable.ServedOutcomeIsCertain(reported.Completion.GetOutcome()))
	// The answer is dated by the owner's own completed_at: a caller's clock
	// records when it heard, which for a report that waited is not when the
	// work ended.
	require.Equal(t, issued.Add(time.Hour), reported.Completion.GetAnsweredAt().AsTime())
}

// A report that does not belong to this work is refused rather than classified.
// Reading another invocation's answer as this one's resolves an invocation with
// an outcome that is not its own, which no later evidence corrects.
func TestAReportMustBelongToTheAcceptedWork(t *testing.T) {
	pkg := submittedPackage(t)
	inv := submittedInvocation(t, pkg)
	accepted := acceptance()
	result := &basev0.RunnableResult{
		Protocol:     runnable.ServedProtocolV1,
		InvocationId: "inv-1",
		Status:       basev0.RunnableResult_SUCCEEDED,
		Output:       []byte(`{"count":4,"longest":"quick"}`),
	}

	for _, tc := range []struct {
		name     string
		document func() []byte
		want     string
	}{
		{
			name: "another handle",
			document: func() []byte {
				return submitDocument(t, &runnablev0.RunnableCompletionCallback{
					Schema: runnable.CallbackSchemaV1, Handle: "owner-work-other", InvocationId: "inv-1",
					EffectId: "effect-1", Result: result, CompletedAt: timestamppb.New(issued.Add(time.Hour)),
				})
			},
			want: "not the accepted",
		},
		{
			name: "another effect identity",
			document: func() []byte {
				return submitDocument(t, &runnablev0.RunnableCompletionCallback{
					Schema: runnable.CallbackSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
					EffectId: "effect-2", Result: result, CompletedAt: timestamppb.New(issued.Add(time.Hour)),
				})
			},
			want: "not the invocation's",
		},
		{
			name: "a shape this address does not receive",
			document: func() []byte {
				return submitDocument(t, acceptance())
			},
			want: "is not a document this address receives",
		},
		{
			name:     "a document naming no schema at all",
			document: func() []byte { return []byte(`{"handle":"owner-work-7f3c"}`) },
			want:     "names no schema",
		},
		{
			name: "a terminal answer carrying no result",
			document: func() []byte {
				return submitDocument(t, &runnablev0.RunnableCompletionCallback{
					Schema: runnable.CallbackSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
					EffectId: "effect-1", CompletedAt: timestamppb.New(issued.Add(time.Hour)),
				})
			},
			want: "result: value is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runnable.ClassifyReport(tc.document(), inv, pkg, accepted)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// The status read is what a caller whose callback never arrived asks, and it is
// the only mechanism a caller that presented no callback address has. Work that
// has not ended answers with no completion, which is the answer: keep waiting.
func TestTheStatusReadAnswersACallerWhoseCallbackNeverArrived(t *testing.T) {
	pkg := submittedPackage(t)
	inv := submittedInvocation(t, pkg)
	accepted := acceptance()

	running := submitDocument(t, &runnablev0.RunnableStatus{
		Schema: runnable.StatusSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
		State:    runnablev0.RunnableStatus_STATE_RUNNING,
		Progress: &runnablev0.RunnableProgress{Done: 1, Note: "still going"},
	})
	status, completion, err := runnable.ClassifyStatus(running, inv, pkg, accepted)
	require.NoError(t, err)
	require.Nil(t, completion, "work that has not ended has no outcome to record")
	require.Equal(t, runnablev0.RunnableStatus_STATE_RUNNING, status.GetState())

	failed := submitDocument(t, &runnablev0.RunnableStatus{
		Schema: runnable.StatusSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
		State: runnablev0.RunnableStatus_STATE_COMPLETED,
		Result: &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1",
			Status: basev0.RunnableResult_FAILED,
			Error:  &basev0.RunnableError{Code: "card_declined", Message: "the issuer declined"},
		},
		CompletedAt: timestamppb.New(issued.Add(time.Hour)),
	})
	_, completion, err = runnable.ClassifyStatus(failed, inv, pkg, accepted)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableServedOutcome_SERVED_OWNER_FAILED, completion.GetOutcome())
	require.Equal(t, "card_declined", completion.GetFailureCode())
	require.True(t, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))

	// An owner that accepted the work and cannot say what became of it reports
	// it rather than withholding it: a caller told "lost" reads the receipt,
	// while a caller told nothing cannot tell this from an owner that never
	// accepted the work at all.
	lost := submitDocument(t, &runnablev0.RunnableStatus{
		Schema: runnable.StatusSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
		State: runnablev0.RunnableStatus_STATE_LOST,
	})
	_, completion, err = runnable.ClassifyStatus(lost, inv, pkg, accepted)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE, completion.GetOutcome())
	require.False(t, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))
	require.Contains(t, completion.GetMessage(), "receipt")

	// A status that says the work ended and carries no answer says the work
	// ended and refuses to say how, which is not something a caller can record.
	silent := submitDocument(t, &runnablev0.RunnableStatus{
		Schema: runnable.StatusSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
		State: runnablev0.RunnableStatus_STATE_COMPLETED, CompletedAt: timestamppb.New(issued.Add(time.Hour)),
	})
	_, _, err = runnable.ClassifyStatus(silent, inv, pkg, accepted)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "refuses to say how")

	// A status that says the work is running and carries an answer for it is
	// two statements about one invocation, and the second would be read as
	// terminal.
	inconsistent := submitDocument(t, &runnablev0.RunnableStatus{
		Schema: runnable.StatusSchemaV1, Handle: accepted.GetHandle(), InvocationId: "inv-1",
		State: runnablev0.RunnableStatus_STATE_ACCEPTED,
		Result: &basev0.RunnableResult{
			Protocol: runnable.ServedProtocolV1, InvocationId: "inv-1",
			Status: basev0.RunnableResult_SUCCEEDED, Output: []byte(`{"count":4,"longest":"quick"}`),
		},
	})
	_, _, err = runnable.ClassifyStatus(inconsistent, inv, pkg, accepted)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

// The callback address is the caller's choice and the owner's instruction, so
// it is held to the rules before the call rather than when a report fails to
// arrive — an owner that cannot reach it reports nothing, and silence is
// indistinguishable from a worker that died.
func TestTheCallbackAddressIsHeldToTheRulesBeforeTheCall(t *testing.T) {
	pkg := submittedPackage(t)

	for _, tc := range []struct {
		name   string
		target *basev0.RunnableCallbackTarget
		want   string
	}{
		{
			name:   "plaintext to a host that may resolve anywhere",
			target: &basev0.RunnableCallbackTarget{Address: "http://runtime.example/reports", Audience: "runtime"},
			want:   "not a loopback host",
		},
		{
			name:   "a scheme nothing POSTs to",
			target: &basev0.RunnableCallbackTarget{Address: "ftp://runtime.example/reports", Audience: "runtime"},
			want:   "not an HTTP address",
		},
		{
			name:   "a credential smuggled into the URL",
			target: &basev0.RunnableCallbackTarget{Address: "https://user:secret@runtime.example/reports", Audience: "runtime"},
			want:   "userinfo",
		},
		{
			name:   "no audience to mint the reporting capability for",
			target: &basev0.RunnableCallbackTarget{Address: "https://runtime.example/reports"},
			want:   "audience",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, runnable.ValidateCallbackTarget(tc.target), tc.want)

			inv := sampleInvocation(pkg)
			inv.EffectId = "effect-1"
			inv.Callback = tc.target
			_, err := runnable.PrepareInvocation(inv, pkg)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}

	// A development caller on this machine is admitted in plaintext, because a
	// loopback address cannot leave it.
	require.NoError(t, runnable.ValidateCallbackTarget(&basev0.RunnableCallbackTarget{
		Address: "http://127.0.0.1:41235/reports", Audience: "runtime",
	}))
	require.NoError(t, runnable.ValidateCallbackTarget(&basev0.RunnableCallbackTarget{
		Address: "http://localhost:41235/reports", Audience: "runtime",
	}))
}

// A call-mode invocation carrying a reporting address describes a report
// nothing ever sends. It is refused where the invocation is prepared, which is
// before anything has been called.
func TestACallModeInvocationCarriesNoCallbackAddress(t *testing.T) {
	pkg := preparedPackage(t)
	inv := sampleInvocation(pkg)
	inv.Callback = &basev0.RunnableCallbackTarget{Address: "https://runtime.example/reports", Audience: "runtime"}

	_, err := runnable.PrepareInvocation(inv, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "answers in the reply to the call")
}

// Submit mode's identity carrier is the one part of this contract that is not
// new: whatever performs the invocation mints at the moment it calls, from the
// grant the invocation carries, because a child capability projected when the
// work was submitted is expired by the time a container starts.
func TestWorkThatStartsLaterCarriesTheGrantRatherThanACapability(t *testing.T) {
	pkg := submittedPackage(t)
	inv := sampleInvocation(pkg)
	inv.EffectId = "effect-1"
	inv.Identity = &basev0.RunnableInvocationIdentity{
		Carrier: &basev0.RunnableInvocationIdentity_Grant{
			Grant: &basev0.RunnableGrantReference{DelegationId: "delegation-9", Audience: "documents"},
		},
	}
	prepared, err := runnable.PrepareInvocation(inv, pkg)
	require.NoError(t, err)
	require.Equal(t, "delegation-9", prepared.GetIdentity().GetGrant().GetDelegationId())
	require.Empty(t, prepared.GetIdentity().GetWorkContext(), "the two carriers are exclusive: one is minted from the other, late")

	// An invocation with neither reaches an owner with no identity, which is
	// the one state the required slot exists to remove.
	inv.Identity = nil
	_, err = runnable.PrepareInvocation(inv, pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}
