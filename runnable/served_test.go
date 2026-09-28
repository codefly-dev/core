package runnable_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/codefly-dev/core/wool"
)

func servedCall() runnable.Call {
	return runnable.Call{
		CalledAt:          issued,
		AnsweredAt:        issued.Add(time.Second),
		AuthorityResolved: true,
		Answered:          true,
	}
}

// TestServedClassificationDrawsTheLineTheProcessTaxonomyDrew is the served
// replacement for the deleted process outcome table, and the reason the native
// placement could be removed at all: the line between a proven and an unproven
// effect is what recovery depends on, so losing it with the launcher would have
// left recovery deciding by itself.
func TestServedClassificationDrawsTheLineTheProcessTaxonomyDrew(t *testing.T) {
	pkg := preparedPackage(t)
	inv, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		call    func(runnable.Call) runnable.Call
		outcome basev0.RunnableServedOutcome
		certain bool
	}{
		{
			name: "a response that validates is proven committed",
			call: func(c runnable.Call) runnable.Call {
				c.Response = succeededDocument(t)
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_SUCCEEDED,
			certain: true,
		},
		{
			name: "the owner's own typed code is proven not committed",
			call: func(c runnable.Call) runnable.Call {
				c.FailureCode = "card_declined"
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_OWNER_FAILED,
			certain: true,
		},
		{
			name: "nothing sent is proven not committed",
			call: func(c runnable.Call) runnable.Call {
				c.AuthorityResolved = false
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_AUTHORITY_UNAVAILABLE,
			certain: true,
		},
		{
			name: "a call that never completed is unproven",
			call: func(c runnable.Call) runnable.Call {
				c.Answered = false
				c.Trouble = errors.New("connection reset")
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE,
			certain: false,
		},
		{
			name: "a container that exits without answering is the same outcome, not its own",
			call: func(c runnable.Call) runnable.Call {
				c.Answered = false
				c.Trouble = errors.New("job container exited without answering")
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE,
			certain: false,
		},
		{
			name: "an answer that is not this invocation's is unproven",
			call: func(c runnable.Call) runnable.Call {
				c.Response = []byte(`{"protocol":"codefly.runnable.served/v1"`)
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY,
			certain: false,
		},
		{
			name: "an interrupted invocation leaves the effect unproven",
			call: func(c runnable.Call) runnable.Call {
				c.Response = resultDocument(t, &basev0.RunnableResult{
					Protocol:     inv.GetProtocol(),
					InvocationId: inv.GetInvocationId(),
					Status:       basev0.RunnableResult_INTERRUPTED,
				})
				return c
			},
			outcome: basev0.RunnableServedOutcome_SERVED_EFFECT_OUTCOME_UNKNOWN,
			certain: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completion, err := runnable.ClassifyServed(inv, pkg, tc.call(servedCall()))
			require.NoError(t, err)
			require.Equal(t, tc.outcome, completion.GetOutcome())
			require.Equal(t, tc.certain, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))
			require.Equal(t, inv.GetInvocationId(), completion.GetInvocationId())
		})
	}
}

// TestABareFailureProvesNothing is the rule the process taxonomy could state
// with an exit status and served mode cannot: a caller must not infer a
// no-effect failure from the shape of an error. Only a code in the protocol's
// own vocabulary is the owner's assertion, and without one the attempt is
// unproven — the side on which it retries, never the side on which it issues the
// effect a second time.
func TestABareFailureProvesNothing(t *testing.T) {
	pkg := preparedPackage(t)
	inv, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	// An owner that completed the call and asserted nothing typed: a 4xx with no
	// failure code, no body. Unproven, and specifically not SERVED_OWNER_FAILED.
	bare := servedCall()
	bare.Trouble = errors.New("owner answered 409 with no typed code")
	completion, err := runnable.ClassifyServed(inv, pkg, bare)
	require.NoError(t, err)
	require.NotEqual(t, basev0.RunnableServedOutcome_SERVED_OWNER_FAILED, completion.GetOutcome())
	require.False(t, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))

	// A FAILED document with an empty code is not a valid result at all — the
	// wire contract requires the code — so it is an integrity failure rather
	// than a proven no-effect one. Either way it stays on the unproven side,
	// which is the property that matters.
	codeless := servedCall()
	codeless.Response = resultDocument(t, &basev0.RunnableResult{
		Protocol:     inv.GetProtocol(),
		InvocationId: inv.GetInvocationId(),
		Status:       basev0.RunnableResult_FAILED,
		Error:        &basev0.RunnableError{Message: "something went wrong"},
	})
	completion, err = runnable.ClassifyServed(inv, pkg, codeless)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY, completion.GetOutcome())
	require.False(t, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))

	// The same answer carrying the owner's own code is the assertion, and is
	// certain.
	coded := servedCall()
	coded.Response = resultDocument(t, &basev0.RunnableResult{
		Protocol:     inv.GetProtocol(),
		InvocationId: inv.GetInvocationId(),
		Status:       basev0.RunnableResult_FAILED,
		Error:        &basev0.RunnableError{Code: "card_declined", Message: "declined"},
	})
	completion, err = runnable.ClassifyServed(inv, pkg, coded)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableServedOutcome_SERVED_OWNER_FAILED, completion.GetOutcome())
	require.Equal(t, "card_declined", completion.GetFailureCode())
	require.True(t, runnable.ServedOutcomeIsCertain(completion.GetOutcome()))
}

// TestClassifyServedRefusesACallItCannotRecord keeps the one line that is an
// error rather than an outcome where it was for a process: a caller told its
// call was not real may issue the effect again, so only a Call that describes no
// call at all is refused.
func TestClassifyServedRefusesACallItCannotRecord(t *testing.T) {
	pkg := preparedPackage(t)
	inv, err := runnable.PrepareInvocation(sampleInvocation(pkg), pkg)
	require.NoError(t, err)

	unbracketed := servedCall()
	unbracketed.AnsweredAt = time.Time{}
	_, err = runnable.ClassifyServed(inv, pkg, unbracketed)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	backwards := servedCall()
	backwards.AnsweredAt = backwards.CalledAt.Add(-time.Second)
	_, err = runnable.ClassifyServed(inv, pkg, backwards)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

// TestServedCallHeadersAreTheOnesTheRuntimeAlreadySends pins the spellings
// rather than the fact that constants exist. A caller and an owner disagreeing
// here fails invisibly: the owner reads no effect id and treats a retry as a new
// effect, or reads no typed failure code and the caller concludes "unproven"
// about a failure the owner did state. These are module-runtime's own values,
// read off its transport, so pinning them changes nothing already running.
func TestServedCallHeadersAreTheOnesTheRuntimeAlreadySends(t *testing.T) {
	require.Equal(t, "Codefly-Runnable-Effect-Id", runnable.EffectHeader)
	require.Equal(t, "Codefly-Runnable-Deadline", runnable.DeadlineHeader)
	require.Equal(t, "Codefly-Runnable-Failure-Code", runnable.FailureCodeHeader)

	// The deadline is written and read in one spelling for the same reason the
	// names are: RFC 3339 with nanoseconds, in UTC, and trailing zeros dropped,
	// so a reader must parse it rather than match a pattern.
	require.Equal(t, "2026-03-04T10:00:00Z", issued.UTC().Format(runnable.DeadlineFormat))
	require.Equal(t, "2026-03-04T10:00:00.123456789Z",
		issued.Add(123456789*time.Nanosecond).UTC().Format(runnable.DeadlineFormat))
}

// TestWorkContextHeaderIsTheOneTheSDKSends is the regression for a defect this
// constant shipped with. #678 pinned "Codefly-Work-Context", believing it was
// adopting what the runtime already sends; the runtime does not set that header
// at all — it calls the SDK, which sets "x-codefly-work-context". HTTP header
// names are case-insensitive, so the two would have compared equal had the only
// difference been case, and they are not: the pinned value was missing the "X-"
// prefix and therefore named a header nobody sets.
//
// Nothing read it yet, so nothing was broken — which is exactly why it needed a
// test. An owner built against the wrong name finds no Work Context, answers
// 401, and every call records SERVED_AUTHORITY_UNAVAILABLE: a failure that looks
// like an authority problem and is a spelling problem.
func TestWorkContextHeaderIsTheOneTheSDKSends(t *testing.T) {
	require.Equal(t, "X-Codefly-Work-Context", runnable.WorkContextHeader)

	// wool carries the same header into gRPC metadata, and core shipped both
	// spellings at once. One value, one place, so they cannot drift again.
	require.Equal(t, wool.WorkContextHeader, runnable.WorkContextHeader)

	// The SDK lowercases it; an HTTP header matches case-insensitively and the
	// gRPC metadata key is the lowercase form, so this is the equality that
	// actually has to hold on the wire.
	require.Equal(t, "x-codefly-work-context", strings.ToLower(runnable.WorkContextHeader))
}

// TestGeneratedHarnessEndpointIsOneSpelling pins where a generated harness
// serves. The caller dials whatever its binding names and imposes no naming
// convention, so this is not a protocol requirement — it is the single spelling
// runnable-go, runnable-python and whoever writes the binding all use, and none
// of those three owns the other two. A harness serving one path while its
// binding names another is an unreachable owner, which reads as an outage
// rather than as a mistake in a string.
func TestGeneratedHarnessEndpointIsOneSpelling(t *testing.T) {
	require.Equal(t, "CODEFLY__RUNNABLE_ADDRESS", runnable.ListenAddressEnv)
	require.Equal(t, "/codefly.runnable.v0.Runnable/Invoke", runnable.ServedInvokeProcedure)
	require.Equal(t, "/codefly.runnable.v0.Runnable/Lookup", runnable.ServedLookupProcedure)

	// Both must satisfy ConnectProcedure.procedure, which is two path segments
	// — a one-segment route would be refused at install rather than at a call,
	// but only after a harness had already been generated serving it.
	procedure := regexp.MustCompile(`^/[A-Za-z_][A-Za-z0-9_.]*/[A-Za-z_][A-Za-z0-9_]*$`)
	require.Regexp(t, procedure, runnable.ServedInvokeProcedure)
	require.Regexp(t, procedure, runnable.ServedLookupProcedure)
	require.NotEqual(t, runnable.ServedInvokeProcedure, runnable.ServedLookupProcedure)
}
