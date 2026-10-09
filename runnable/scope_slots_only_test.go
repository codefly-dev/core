package runnable_test

import (
	"os"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func slotOnlyOperation(t *testing.T) *runnablev0.Operation {
	t.Helper()
	value, err := os.ReadFile("testdata/scope-slots/slot-only-operation.json")
	require.NoError(t, err)
	declared, err := runnable.DecodeOperation(value)
	require.NoError(t, err)
	return declared
}

func slotOnlySelection() []*runnablev0.ScopeSelection {
	return []*runnablev0.ScopeSelection{{
		Slot:   "target",
		Invoke: []*basev0.WorkScopeV1{scope("acme.item", []string{"item-a"}, "invoke", "read")},
		Lookup: []*basev0.WorkScopeV1{scope("acme.item", []string{"item-a"}, "read")},
	}}
}

func TestScopeSlotsCanSupplyAllOperationAuthority(t *testing.T) {
	for _, lookup := range []bool{true, false} {
		t.Run(map[bool]string{true: "required lookup", false: "optional lookup"}[lookup], func(t *testing.T) {
			declared := slotOnlyOperation(t)
			declared.RequiredScopeSlots[0].Lookup = lookup
			_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.NoError(t, err)
			require.Empty(t, spec.InvokeScopes)
			require.Empty(t, spec.LookupScopes)
			require.True(t, proto.Equal(declared, spec.Policy()))

			// Both delivery surfaces refuse the unresolved declaration, even when
			// its only authority is the slot. Supplying digests cannot bypass it.
			pending := bindingFor(spec)
			pending.Schema = runnable.PreparedSchemaV3
			pending.ContractDigest, err = runnable.ContractDigest(pending.Contract)
			require.NoError(t, err)
			require.ErrorIs(t, runnable.VerifyPrepared(pending), runnable.ErrUnresolvedScopeSlots)
			_, err = runnable.EncodePrepared(pending)
			require.ErrorIs(t, err, runnable.ErrUnresolvedScopeSlots)
			receipt := receiptFor(pending)
			receipt.Schema = runnable.ResolvedPolicySchemaV1
			receipt.PolicyDigest, err = runnable.PolicyDigest(receipt.Policy)
			require.NoError(t, err)
			require.ErrorIs(t, runnable.VerifyResolvedPolicy(receipt), runnable.ErrUnresolvedScopeSlots)
			_, err = runnable.EncodeResolvedPolicy(receipt)
			require.ErrorIs(t, err, runnable.ErrUnresolvedScopeSlots)

			selections := slotOnlySelection()
			resolved, err := spec.ResolveScopeSlots(selections)
			require.NoError(t, err)
			require.Empty(t, resolved.RequiredScopeSlots)
			require.Len(t, resolved.InvokeScopes, 1)
			require.Len(t, resolved.LookupScopes, 1)
			require.True(t, proto.Equal(selections[0].Invoke[0], resolved.InvokeScopes[0]))
			require.True(t, proto.Equal(selections[0].Lookup[0], resolved.LookupScopes[0]))
			delivered, err := runnable.DecodePrepared(encoded(t, bindingFor(resolved)))
			require.NoError(t, err)
			require.NoError(t, runnable.VerifyPrepared(delivered))
			value, err := runnable.EncodeResolvedPolicy(receiptFor(delivered))
			require.NoError(t, err)
			installed, err := runnable.DecodeResolvedPolicy(value)
			require.NoError(t, err)
			require.NoError(t, runnable.BindingMatchesResolvedPolicy(delivered, installed))
			_, err = runnable.ToolFromPrepared(delivered)
			require.ErrorIs(t, err, runnable.ErrNotATool)

			for _, clearInvoke := range []bool{true, false} {
				empty := proto.CloneOf(delivered)
				if clearInvoke {
					empty.Policy.InvokeScopes = nil
				} else {
					empty.Policy.LookupScopes = nil
				}
				require.ErrorIs(t, runnable.VerifyPrepared(empty), runnable.ErrInvalid)
				_, err = runnable.EncodeResolvedPolicy(receiptFor(empty))
				require.ErrorIs(t, err, runnable.ErrInvalid)
			}

			// No mutation of declaration or selection, and no invented fixed scope.
			require.True(t, proto.Equal(declared, spec.Policy()))
			resolved.InvokeScopes[0].ResourceIds[0] = "changed"
			require.Equal(t, "item-a", selections[0].Invoke[0].ResourceIds[0])
			require.Equal(t, "item-a", delivered.Policy.InvokeScopes[0].ResourceIds[0])
		})
	}
}

func TestSlotOnlyAuthorityStillRefusesIncompleteOrWidenedSelections(t *testing.T) {
	for name, change := range map[string]func(*runnablev0.Operation, []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection{
		"missing slot": func(_ *runnablev0.Operation, _ []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection { return nil },
		"empty invoke": func(_ *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Invoke = nil
			return s
		},
		"missing required lookup": func(_ *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup = nil
			return s
		},
		"empty final lookup": func(o *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			o.RequiredScopeSlots[0].Lookup = false
			s[0].Lookup = nil
			return s
		},
		"wildcard ids": func(_ *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Invoke[0].ResourceIds = nil
			return s
		},
		"widened lookup": func(_ *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup[0].ResourceIds = []string{"item-b"}
			return s
		},
		"write lookup": func(_ *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup[0].Actions = []string{"invoke"}
			return s
		},
		"kind already fixed": func(o *runnablev0.Operation, s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			o.InvokeScopes = []*basev0.WorkScopeV1{proto.CloneOf(s[0].Invoke[0])}
			o.LookupScopes = []*basev0.WorkScopeV1{proto.CloneOf(s[0].Lookup[0])}
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			declared := slotOnlyOperation(t)
			selections := change(declared, slotOnlySelection())
			_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.NoError(t, err, "the declaration is valid before checking its selection")
			_, err = spec.ResolveScopeSlots(selections)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			if name == "empty final lookup" {
				require.ErrorContains(t, err, "declares 0 lookup_scopes")
			}
		})
	}
}

func TestSlotOnlyAuthorityDoesNotRelaxFixedScopes(t *testing.T) {
	for name, change := range map[string]func(*runnablev0.Operation){
		"no slots or scopes":     func(o *runnablev0.Operation) { o.RequiredScopeSlots = nil },
		"malformed slot":         func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].Name = "" },
		"malformed fixed invoke": func(o *runnablev0.Operation) { o.InvokeScopes = []*basev0.WorkScopeV1{{ResourceKind: "acme.fixed"}} },
		"fixed lookup without fixed invoke": func(o *runnablev0.Operation) {
			o.LookupScopes = []*basev0.WorkScopeV1{scope("acme.item", []string{"item-a"}, "read")}
		},
		"fixed lookup widens invoke": func(o *runnablev0.Operation) {
			o.InvokeScopes = []*basev0.WorkScopeV1{scope("acme.fixed", []string{"item-a"}, "invoke", "read")}
			o.LookupScopes = []*basev0.WorkScopeV1{scope("acme.fixed", []string{"item-b"}, "read")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			declared := slotOnlyOperation(t)
			change(declared)
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}
}
