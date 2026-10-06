package runnable_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

func receiptFor(binding *runnablev0.PreparedBinding) *runnablev0.ResolvedPolicy {
	return &runnablev0.ResolvedPolicy{Operation: proto.CloneOf(binding.GetOperation()), Policy: proto.CloneOf(binding.GetPolicy())}
}

// One resolution, two halves: the receipt the host binds from and the binding
// the caller receives carry the same concrete policy, and the gate says so by
// digest and by value — or names what differs.
func TestAResolvedPolicyReceiptHoldsTheBindingToOneResolution(t *testing.T) {
	spec := slottedSpec(t)
	resolved, err := spec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	binding := bindingFor(resolved)

	value, err := runnable.EncodeResolvedPolicy(receiptFor(binding))
	require.NoError(t, err)
	receipt, err := runnable.DecodeResolvedPolicy(value)
	require.NoError(t, err)
	require.Equal(t, runnable.ResolvedPolicySchemaV1, receipt.GetSchema())
	digest, err := runnable.PolicyDigest(resolved.Policy())
	require.NoError(t, err)
	require.Equal(t, digest, receipt.GetPolicyDigest())
	require.True(t, proto.Equal(resolved.Policy(), receipt.GetPolicy()))

	delivered, err := runnable.DecodePrepared(encoded(t, binding))
	require.NoError(t, err)
	require.NoError(t, runnable.BindingMatchesResolvedPolicy(delivered, receipt))

	// The same selection resolved again is the same receipt, byte for byte.
	again, err := spec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	value2, err := runnable.EncodeResolvedPolicy(receiptFor(bindingFor(again)))
	require.NoError(t, err)
	require.Equal(t, string(value), string(value2))

	// A binding whose policy was generated from anything else is named.
	other := bindingFor(resolved)
	other.Policy.InvokeScopes[1].ResourceIds = append(other.Policy.InvokeScopes[1].ResourceIds, "model-c")
	drifted, err := runnable.DecodePrepared(encoded(t, other))
	require.NoError(t, err)
	err = runnable.BindingMatchesResolvedPolicy(drifted, receipt)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "were not generated from one resolution")

	// The owner's fixed-only policy — what a writer that lost the slots would
	// deliver — is not the resolved one either.
	fixedOnly := connectBinding()
	fixedOnly.Operation = proto.CloneOf(binding.Operation)
	fixedOnly.Call = proto.CloneOf(binding.Call)
	fixedOnly.Policy = declaredOperation()
	lost, err := runnable.DecodePrepared(encoded(t, fixedOnly))
	require.NoError(t, err)
	require.ErrorIs(t, runnable.BindingMatchesResolvedPolicy(lost, receipt), runnable.ErrInvalid)

	// A different operation with the same policy is a different installation.
	elsewhere := receiptFor(binding)
	elsewhere.Operation.Service = "other"
	otherValue, err := runnable.EncodeResolvedPolicy(elsewhere)
	require.NoError(t, err)
	otherReceipt, err := runnable.DecodeResolvedPolicy(otherValue)
	require.NoError(t, err)
	err = runnable.BindingMatchesResolvedPolicy(delivered, otherReceipt)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "is for operation")

	// A REST operation's receipt reads its codes in the vocabulary its spelling implies.
	rest := restBinding()
	restValue, err := runnable.EncodeResolvedPolicy(receiptFor(rest))
	require.NoError(t, err)
	restReceipt, err := runnable.DecodeResolvedPolicy(restValue)
	require.NoError(t, err)
	restDelivered, err := runnable.DecodePrepared(encoded(t, rest))
	require.NoError(t, err)
	require.NoError(t, runnable.BindingMatchesResolvedPolicy(restDelivered, restReceipt))
}

func TestAResolvedPolicyReceiptIsRefusedWhenItIsNotAnAnswer(t *testing.T) {
	spec := slottedSpec(t)
	resolved, err := spec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	good := receiptFor(bindingFor(resolved))

	// Slots still open: the receipt still asks the question.
	open := receiptFor(bindingFor(spec))
	_, err = runnable.EncodeResolvedPolicy(open)
	require.ErrorIs(t, err, runnable.ErrUnresolvedScopeSlots)

	// A stated digest or schema that is not the right one is refused, not repaired.
	stated := proto.CloneOf(good)
	stated.PolicyDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	_, err = runnable.EncodeResolvedPolicy(stated)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	stated = proto.CloneOf(good)
	stated.Schema = "codefly.runnable-resolved-policy/v0"
	_, err = runnable.EncodeResolvedPolicy(stated)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	// A document carrying a field this reader does not declare is refused.
	value, err := runnable.EncodeResolvedPolicy(good)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &fields))
	fields["selections"] = json.RawMessage(`[]`)
	foreign, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = runnable.DecodeResolvedPolicy(foreign)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	// A policy outside the installation bounds is refused as a binding's would be.
	wide := proto.CloneOf(good)
	wide.Policy.MaxAttempts = 99
	_, err = runnable.EncodeResolvedPolicy(wide)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	_, err = runnable.EncodeResolvedPolicy(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	_, err = runnable.DecodeResolvedPolicy(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorIs(t, runnable.VerifyResolvedPolicy(nil), runnable.ErrInvalid)
}
