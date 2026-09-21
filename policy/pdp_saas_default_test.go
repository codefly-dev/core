package policy

import (
	"context"
	"testing"
)

func TestSaasPDPZeroValueDeniesBackendFailure(t *testing.T) {
	var pdp SaasPDP
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if decision := pdp.failClosedDecision(err); decision.Allow {
			t.Fatalf("zero-value error policy allowed a request after %v: %+v", err, decision)
		}
	}
}
