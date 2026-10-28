package runnable_test

import (
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestOneAttemptCallRequiresVerifiedExactPolicy(t *testing.T) {
	binding := connectBinding()
	binding.Policy.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
	binding.Policy.MaxAttempts = 1
	binding.Policy.RetryableCodes = nil
	binding.Policy.LookupScopes = nil
	admitted, err := runnable.DecodePrepared(encoded(t, binding))
	require.NoError(t, err)
	require.NoError(t, runnable.ValidateOneAttemptCall(admitted))
	require.Error(t, runnable.ValidateOneAttemptCall(nil))
	for name, change := range map[string]func(*runnablev0.PreparedBinding){
		"missing digest":       func(b *runnablev0.PreparedBinding) { b.ContractDigest = "" },
		"changed contract":     func(b *runnablev0.PreparedBinding) { b.Contract.Output.Fields[0].Name = "changed" },
		"wrong route":          func(b *runnablev0.PreparedBinding) { b.Call.GetConnect().Procedure = "/other.Service/Method" },
		"no audience":          func(b *runnablev0.PreparedBinding) { b.Policy.Audience = "" },
		"no invoke authority":  func(b *runnablev0.PreparedBinding) { b.Policy.InvokeScopes = nil },
		"unspecified recovery": func(b *runnablev0.PreparedBinding) { b.Policy.Recovery = basev0.RunnableExecution_RECOVERY_UNKNOWN },
		"recompute":            func(b *runnablev0.PreparedBinding) { b.Policy.Recovery = basev0.RunnableExecution_RECOVERY_RECOMPUTE },
		"submit":               func(b *runnablev0.PreparedBinding) { b.Policy.Completion = basev0.RunnableExecution_COMPLETION_SUBMIT },
		"no completion":        func(b *runnablev0.PreparedBinding) { b.Policy.Completion = basev0.RunnableExecution_COMPLETION_UNKNOWN },
		"two attempts":         func(b *runnablev0.PreparedBinding) { b.Policy.MaxAttempts = 2 },
		"retry code":           func(b *runnablev0.PreparedBinding) { b.Policy.RetryableCodes = []string{"UNAVAILABLE"} },
		"lookup route":         func(b *runnablev0.PreparedBinding) { b.Policy.LookupMethod = "/acme.items.v1.Items/LookupApply" },
		"lookup authority":     func(b *runnablev0.PreparedBinding) { b.Policy.LookupScopes = b.Policy.InvokeScopes },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := proto.CloneOf(admitted)
			change(candidate)
			require.Error(t, runnable.ValidateOneAttemptCall(candidate))
		})
	}
}
