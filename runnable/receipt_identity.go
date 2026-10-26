package runnable

import (
	"errors"
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	receiptsv0 "github.com/codefly-dev/core/generated/go/codefly/runnable/receipts/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/workcontext"
)

// ErrReceiptNotAdmitted refuses a receipt lookup by a caller that did not make
// the effect, or that does not hold the authority the receipt is read under.
// A server answers it exactly as it answers an absent receipt, so a refusal
// never confirms that someone else's effect exists.
var ErrReceiptNotAdmitted = errors.New("receipt lookup is not admitted for this caller")

// ReceiptIdentityOf is the identity a receipt records: the Work Context's
// owner and its acting principal, the last actor of the chain or the owner
// when nobody was delegated to.
func ReceiptIdentityOf(context *basev0.WorkContextV1) (*receiptsv0.ReceiptIdentity, error) {
	owner := context.GetOwnerPrincipalId()
	if owner == "" {
		return nil, fmt.Errorf("%w: a receipt is recorded under a verified Work Context, and this one names no owner", ErrInvalid)
	}
	acting := owner
	if chain := context.GetActorChain(); len(chain) > 0 {
		acting = chain[len(chain)-1].GetPrincipalId()
	}
	if acting == "" {
		return nil, fmt.Errorf("%w: the Work Context's current actor names no principal", ErrInvalid)
	}
	return &receiptsv0.ReceiptIdentity{OwnerPrincipalId: owner, ActingPrincipalId: acting}, nil
}

// AdmitReceiptLookup is the one decision a Receipts server makes for a lookup
// of a receipt it holds, after resolving the tenant from the presented
// context. It admits the lookup only when:
//
//   - the operation recovers by receipt (a recomputed read has none);
//   - the presented context is the identity the receipt recorded, owner and
//     acting principal both; and
//   - its effective scopes cover the operation's lookup scopes, or, when the
//     operation declares none, its invoke scopes — the authority that made the
//     effect, never more.
//
// Every refusal wraps ErrReceiptNotAdmitted.
func AdmitReceiptLookup(policy *runnablev0.Operation, recorded *receiptsv0.ReceiptIdentity, presented *basev0.WorkContextV1, effective []*basev0.WorkScopeV1) error {
	if policy.GetRecovery() == basev0.RunnableExecution_RECOVERY_RECOMPUTE {
		return fmt.Errorf("%w: the operation is recomputed and keeps no receipt", ErrReceiptNotAdmitted)
	}
	if recorded.GetOwnerPrincipalId() == "" || recorded.GetActingPrincipalId() == "" {
		return fmt.Errorf("%w: the receipt records no identity, so no caller can be shown to have made it", ErrReceiptNotAdmitted)
	}
	caller, err := ReceiptIdentityOf(presented)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReceiptNotAdmitted, err)
	}
	if caller.GetOwnerPrincipalId() != recorded.GetOwnerPrincipalId() || caller.GetActingPrincipalId() != recorded.GetActingPrincipalId() {
		return fmt.Errorf("%w: the receipt was recorded for another caller", ErrReceiptNotAdmitted)
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
