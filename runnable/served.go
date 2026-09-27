package runnable

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Call is what a caller saw of one call to a served operation. It is raw
// observation, exactly as Observation was for a process: ClassifyServed turns it
// into the typed outcome, so every caller of a served operation draws the line
// between a proven and an unproven effect in the same place.
type Call struct {
	// CalledAt and AnsweredAt bracket the call. AnsweredAt is when the caller
	// stopped waiting, which for a call that never got an answer is when it gave
	// up rather than when anything replied.
	CalledAt   time.Time
	AnsweredAt time.Time
	// AuthorityResolved says an authority was minted and the call was sent. It
	// is false only when nothing left the caller, which is the one way a caller
	// proves a no-effect failure without the owner saying so.
	AuthorityResolved bool
	// Answered says the call completed with an answer from the owner, whatever
	// that answer was. A dropped connection, a timeout and a container that
	// exited without replying are all false, and are the same outcome: the
	// caller never had a way to tell them apart.
	Answered bool
	// Response is the document the owner answered with, nil when it answered
	// nothing. It is separate from Answered because an owner may complete a call
	// and return no body, which is not the same as never completing.
	Response []byte
	// FailureCode is the owner's typed failure code as the protocol carried it —
	// a value in the protocol's own vocabulary, never inferred from a status
	// class. Empty means the owner asserted no typed failure, whatever status it
	// used, which is why a bare 4xx is unproven.
	FailureCode string
	// Trouble records what went wrong for an outcome carrying no result.
	Trouble error
}

// ClassifyServed turns what a caller saw into the typed served outcome.
//
// It never returns an error for anything the owner did: every way a call can end
// is an outcome, because a caller told "this was not a real call" may issue the
// effect a second time. It errors only on a Call it cannot record at all, the
// same line validateObservation drew for a process.
func ClassifyServed(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, observed Call) (*basev0.RunnableServedCompletion, error) {
	if err := validateInvocation(inv, pkg); err != nil {
		return nil, err
	}
	if observed.CalledAt.IsZero() || observed.AnsweredAt.IsZero() {
		return nil, fmt.Errorf("%w: call must be bracketed by a start and an end", ErrInvalid)
	}
	if observed.AnsweredAt.Before(observed.CalledAt) {
		return nil, fmt.Errorf("%w: call is answered at %s, before it was sent at %s",
			ErrInvalid, observed.AnsweredAt, observed.CalledAt)
	}
	completion := &basev0.RunnableServedCompletion{
		Runnable:     proto.CloneOf(pkg.GetIdentity()),
		InvocationId: inv.GetInvocationId(),
		CalledAt:     timestamppb.New(observed.CalledAt),
		AnsweredAt:   timestamppb.New(observed.AnsweredAt),
	}
	if observed.Trouble != nil {
		completion.Message = observed.Trouble.Error()
	}
	switch {
	case !observed.AuthorityResolved:
		// Nothing was sent, so the effect did not happen. This is the only
		// certainty a caller reaches on its own; every other proof is the
		// owner's own assertion.
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_AUTHORITY_UNAVAILABLE
		if completion.GetMessage() == "" {
			completion.Message = "no Work Context was resolved for the call, so it was never sent"
		}
		return completion, nil
	case !observed.Answered:
		// The call did not complete. A container that exited without answering
		// is here too, and so is a reply that was lost: the receipt is the only
		// evidence in every one of those cases.
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE
		return completion, nil
	case observed.FailureCode != "":
		// The owner's own typed assertion that it completed without its effect.
		// The code is carried verbatim: it is the evidence the outcome rests on.
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_OWNER_FAILED
		completion.FailureCode = observed.FailureCode
		completion.Result = &basev0.RunnableResult{
			Protocol:     inv.GetProtocol(),
			InvocationId: inv.GetInvocationId(),
			Status:       basev0.RunnableResult_FAILED,
			Error:        &basev0.RunnableError{Code: observed.FailureCode, Message: completion.GetMessage()},
		}
		return completion, nil
	}
	result, err := ParseResult(observed.Response, inv, pkg)
	if err != nil {
		// The owner answered with something that is not this invocation's valid
		// response. The effect may have committed before the answer was spoiled,
		// so this is unproven rather than a failure.
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY
		completion.Message = appendServedMessage(completion.GetMessage(), err.Error())
		return completion, nil
	}
	completion.Result = result
	switch result.GetStatus() {
	case basev0.RunnableResult_SUCCEEDED:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_SUCCEEDED
	case basev0.RunnableResult_FAILED:
		// ParseResult already refused a FAILED result carrying no code, so the
		// owner's assertion is present by the time we are here.
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_OWNER_FAILED
		completion.FailureCode = result.GetError().GetCode()
	case basev0.RunnableResult_INTERRUPTED:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_EFFECT_OUTCOME_UNKNOWN
	default:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY
	}
	return completion, nil
}

// ServedOutcomeIsCertain says whether the outcome proves what happened to the
// effect. It is the served counterpart of OutcomeIsCertain and draws the same
// line: three outcomes are certain, and the rest are resolved by the package's
// recovery policy.
//
// SERVED_AUTHORITY_UNAVAILABLE is certain because nothing was sent.
// SERVED_OWNER_FAILED is certain because the owner asserted it in its own
// vocabulary — which is why ClassifyServed refuses to reach it from a status
// class. Everything else, including an interrupted invocation, is unproven.
func ServedOutcomeIsCertain(outcome basev0.RunnableServedOutcome) bool {
	switch outcome {
	case basev0.RunnableServedOutcome_SERVED_SUCCEEDED,
		basev0.RunnableServedOutcome_SERVED_OWNER_FAILED,
		basev0.RunnableServedOutcome_SERVED_AUTHORITY_UNAVAILABLE:
		return true
	default:
		return false
	}
}

func appendServedMessage(message, addition string) string {
	if message == "" {
		return addition
	}
	return message + "; " + addition
}
