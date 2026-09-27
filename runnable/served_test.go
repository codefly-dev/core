package runnable_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
)

func servedCall() runnable.Call {
	return runnable.Call{
		CalledAt:          issued,
		AnsweredAt:        issued.Add(time.Second),
		AuthorityResolved: true,
		Answered:          true,
	}
}

// TestServedClassificationDrawsTheLineTheLauncherDrew is the served replacement
// for the launcher's outcome table, and the reason the native placement could be
// removed at all: OutcomeIsCertain's line between a proven and an unproven
// effect is what recovery depends on, so losing it with the launcher would have
// left recovery deciding by itself.
func TestServedClassificationDrawsTheLineTheLauncherDrew(t *testing.T) {
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
				c.Response = []byte(`{"protocol":"codefly.runnable/v1"`)
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

// TestABareFailureProvesNothing is the rule the launcher taxonomy could state
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
