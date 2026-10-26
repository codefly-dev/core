package runnable

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	receiptsv0 "github.com/codefly-dev/core/generated/go/codefly/runnable/receipts/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/workcontext"
)

// ErrReceiptNotAdmitted refuses a receipt lookup by a caller that did not make
// the effect, or that does not hold the authority the receipt is read under.
// A server answers it exactly as it answers an absent receipt — NOT_FOUND —
// and logs the reason rather than returning it, so a refusal never confirms
// that someone else's effect exists.
var ErrReceiptNotAdmitted = errors.New("receipt lookup is not admitted for this caller")

// ErrReceiptConflict refuses a guarded invocation whose effect id already
// holds a receipt recorded for another identity. It carries no content: the
// owner neither runs the effect nor replays the other caller's committed
// response, and answers the conflict without saying whose receipt it is.
var ErrReceiptConflict = errors.New("effect id holds a receipt recorded for another caller")

// ReceiptIdentityOf is the identity a receipt records: the Work Context's
// owner and its whole actor chain, each hop by principal and agent.
func ReceiptIdentityOf(context *basev0.WorkContextV1) (*receiptsv0.ReceiptIdentity, error) {
	if context.GetOwnerPrincipalId() == "" {
		return nil, fmt.Errorf("%w: a receipt is recorded under a verified Work Context, and this one names no owner", ErrInvalid)
	}
	identity := &receiptsv0.ReceiptIdentity{OwnerPrincipalId: context.GetOwnerPrincipalId(), OwnerAgentId: context.GetOwnerAgentId()}
	for _, actor := range context.GetActorChain() {
		if actor.GetPrincipalId() == "" {
			return nil, fmt.Errorf("%w: an actor of the Work Context names no principal", ErrInvalid)
		}
		identity.Actors = append(identity.Actors, &receiptsv0.ReceiptActor{PrincipalId: actor.GetPrincipalId(), AgentId: actor.GetAgentId()})
	}
	return identity, nil
}

// sameIdentity reports the presented context to be exactly the identity a
// receipt recorded. A receipt recording no owner matches nobody.
func sameIdentity(recorded *receiptsv0.ReceiptIdentity, presented *basev0.WorkContextV1) (bool, error) {
	if recorded.GetOwnerPrincipalId() == "" {
		return false, nil
	}
	caller, err := ReceiptIdentityOf(presented)
	if err != nil {
		return false, err
	}
	return proto.Equal(caller, recorded), nil
}

// AdmitReceiptLookup is the one decision a Receipts server makes for a lookup
// of a receipt it holds, after resolving the tenant from the presented
// context. It admits the lookup only when:
//
//   - the operation recovers by receipt (a recomputed read has none);
//   - the presented context is the identity the receipt recorded — owner and
//     whole actor chain; and
//   - its effective scopes cover the operation's lookup scopes, or, when the
//     operation declares none, its invoke scopes — the authority that made the
//     effect, never more.
//
// Every refusal wraps ErrReceiptNotAdmitted.
func AdmitReceiptLookup(policy *runnablev0.Operation, recorded *receiptsv0.ReceiptIdentity, presented *basev0.WorkContextV1, effective []*basev0.WorkScopeV1) error {
	if policy.GetRecovery() == basev0.RunnableExecution_RECOVERY_RECOMPUTE {
		return fmt.Errorf("%w: the operation is recomputed and keeps no receipt", ErrReceiptNotAdmitted)
	}
	same, err := sameIdentity(recorded, presented)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReceiptNotAdmitted, err)
	}
	if !same {
		return fmt.Errorf("%w: the receipt was not recorded for this caller", ErrReceiptNotAdmitted)
	}
	required := policy.GetLookupScopes()
	if len(required) == 0 {
		required = policy.GetInvokeScopes()
	}
	if len(required) == 0 {
		return fmt.Errorf("%w: the operation declares no authority to read its receipt under", ErrReceiptNotAdmitted)
	}
	for _, scope := range required {
		if !workcontext.ScopeContained(scope, effective) {
			return fmt.Errorf("%w: the caller does not hold %s %v", ErrReceiptNotAdmitted, scope.GetResourceKind(), scope.GetActions())
		}
	}
	return nil
}

// AdmitReceiptReplay is the decision a receipt guard makes when an invocation
// presents an effect id that already holds a receipt: the committed response
// may be replayed only to the identity it was recorded for. Any other caller
// is refused with ErrReceiptConflict, which carries no content — its effect
// does not run, and it is never answered with another caller's response.
func AdmitReceiptReplay(recorded *receiptsv0.ReceiptIdentity, presented *basev0.WorkContextV1) error {
	same, err := sameIdentity(recorded, presented)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReceiptConflict, err)
	}
	if !same {
		return ErrReceiptConflict
	}
	return nil
}
