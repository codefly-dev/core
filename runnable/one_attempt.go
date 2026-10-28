package runnable

import (
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// ValidateOneAttemptCall admits a verified binding for a caller that can make
// one synchronous attempt but cannot recover its receipt. RECEIPT still forbids
// recomputation: a lost or invalid answer remains unknown. The caller must
// durably prevent a second dispatch and must not invent a lookup or a new effect.
// This validates policy, not those caller capabilities or the owner's execution.
func ValidateOneAttemptCall(binding *runnablev0.PreparedBinding) error {
	if err := VerifyPrepared(binding); err != nil {
		return err
	}
	p := binding.GetPolicy()
	if p.GetRecovery() != basev0.RunnableExecution_RECOVERY_RECEIPT ||
		p.GetCompletion() != basev0.RunnableExecution_COMPLETION_CALL ||
		p.GetMaxAttempts() != 1 || len(p.GetRetryableCodes()) != 0 ||
		p.GetLookupMethod() != "" || len(p.GetLookupScopes()) != 0 {
		return fmt.Errorf("%w: one-attempt call requires explicit RECEIPT, CALL, one attempt, no retryable codes and no lookup method or scopes", ErrInvalid)
	}
	return nil
}
