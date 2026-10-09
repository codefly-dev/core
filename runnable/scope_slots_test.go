package runnable_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

// slottedOperation is the ingestion policy with the authority a generic model
// or tool operation cannot spell alone: the owner names two slots and states
// what it needs of them; which kinds, actions and ids fill them is the
// composition's.
func slottedOperation() *runnablev0.Operation {
	declared := declaredOperation()
	declared.RequiredScopeSlots = []*runnablev0.ScopeSlot{
		{Name: "model", RequiredActions: []string{"invoke", "read"}, Lookup: true},
		{Name: "tools"},
	}
	return declared
}

func scope(kind string, ids []string, actions ...string) *basev0.WorkScopeV1 {
	return &basev0.WorkScopeV1{ResourceKind: kind, Actions: actions, ResourceIds: ids}
}

// slotSelections is a composition's answer: a model kind another module
// defines, and two tool kinds in their owners' own vocabularies, one of which
// has no read-only action and so binds no lookup.
func slotSelections() []*runnablev0.ScopeSelection {
	return []*runnablev0.ScopeSelection{
		{
			Slot:   "model",
			Invoke: []*basev0.WorkScopeV1{scope("robin/model", []string{"model-a", "model-b"}, "invoke", "read")},
			Lookup: []*basev0.WorkScopeV1{scope("robin/model", []string{"model-a", "model-b"}, "read")},
		},
		{
			Slot: "tools",
			Invoke: []*basev0.WorkScopeV1{
				scope("acme.tool/search", []string{"search-1"}, "call"),
				scope("acme.tool/calendar", []string{"cal-1", "cal-2"}, "call", "read"),
			},
			Lookup: []*basev0.WorkScopeV1{scope("acme.tool/calendar", []string{"cal-1"}, "read")},
		},
	}
}

func slottedSpec(t *testing.T) *runnable.OperationSpec {
	t.Helper()
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, slottedOperation()), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	return spec
}

func bindingFor(spec *runnable.OperationSpec) *runnablev0.PreparedBinding {
	binding := connectBinding()
	binding.Operation.Spelling = applyText
	binding.Call.Route = &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: applyText}}
	binding.Policy = spec.Policy()
	return binding
}

func TestScopeSlotsAreDeclaredByTheOwnerAndResolvedByTheComposition(t *testing.T) {
	spec := slottedSpec(t)
	require.Len(t, spec.RequiredScopeSlots, 2, "derivation carries the owner's slots")

	// A hole in the authority is not a policy a binding may deliver.
	_, err := runnable.EncodePrepared(bindingFor(spec))
	require.ErrorIs(t, err, runnable.ErrUnresolvedScopeSlots)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "model, tools")

	resolved, err := spec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	require.Empty(t, resolved.RequiredScopeSlots)
	require.Len(t, resolved.InvokeScopes, 4)
	require.Len(t, resolved.LookupScopes, 3)
	require.True(t, proto.Equal(spec.InvokeScopes[0], resolved.InvokeScopes[0]), "the owner's fixed scope comes first, unchanged")
	require.True(t, proto.Equal(scope("robin/model", []string{"model-a", "model-b"}, "invoke", "read"), resolved.InvokeScopes[1]))
	require.True(t, proto.Equal(scope("acme.tool/search", []string{"search-1"}, "call"), resolved.InvokeScopes[2]))
	require.True(t, proto.Equal(scope("acme.tool/calendar", []string{"cal-1", "cal-2"}, "call", "read"), resolved.InvokeScopes[3]))
	require.True(t, proto.Equal(spec.LookupScopes[0], resolved.LookupScopes[0]))
	require.True(t, proto.Equal(scope("robin/model", []string{"model-a", "model-b"}, "read"), resolved.LookupScopes[1]),
		"a slot that asks for lookup binds the read-only subset over the same exact ids")
	require.True(t, proto.Equal(scope("acme.tool/calendar", []string{"cal-1"}, "read"), resolved.LookupScopes[2]),
		"a slot that does not ask for lookup still accepts a narrower one the composition binds")

	// The originals are read, never written.
	require.Len(t, spec.RequiredScopeSlots, 2)
	require.Len(t, spec.InvokeScopes, 1)
	require.Len(t, spec.LookupScopes, 1)
	selections := slotSelections()
	again, err := spec.ResolveScopeSlots(selections)
	require.NoError(t, err)
	again.InvokeScopes[1].ResourceIds[0] = "changed"
	again.InvokeScopes[0].Actions[0] = "changed"
	require.Equal(t, "model-a", selections[0].Invoke[0].ResourceIds[0])
	require.Equal(t, "ingest", spec.InvokeScopes[0].Actions[0])

	// The concrete policy is what a binding delivers and what a reader gets
	// back, and resolving the same selection again yields the same policy: one
	// resolution serves every place the policy appears.
	delivered, err := runnable.DecodePrepared(encoded(t, bindingFor(resolved)))
	require.NoError(t, err)
	require.True(t, proto.Equal(resolved.Policy(), delivered.GetPolicy()))
	require.Empty(t, delivered.GetPolicy().GetRequiredScopeSlots())
	fresh, err := spec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	require.True(t, proto.Equal(resolved.Policy(), fresh.Policy()))

	// The OpenAPI marker is the same schema: slots travel on the REST form too.
	declared := slottedOperation()
	declared.LookupMethod = ""
	declared.RetryableCodes = []string{"503"}
	marker, err := protojson.Marshal(declared)
	require.NoError(t, err)
	restSpec, err := runnable.OperationFromOpenAPIMarker(marker, restSpelling)
	require.NoError(t, err)
	require.Len(t, restSpec.RequiredScopeSlots, 2)
	restResolved, err := restSpec.ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	rest := restBinding()
	rest.Policy = restResolved.Policy()
	_, err = runnable.EncodePrepared(rest)
	require.NoError(t, err)
}

func TestAnOperationWithoutSlotsRefusesASelection(t *testing.T) {
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	resolved, err := spec.ResolveScopeSlots(nil)
	require.NoError(t, err)
	require.True(t, proto.Equal(spec.Policy(), resolved.Policy()), "nothing to resolve yields the same policy")
	_, err = spec.ResolveScopeSlots(slotSelections())
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), `selects scope slot "model", which it does not declare`)
}

func TestScopeSlotDeclarationsAreRefusedWhenMalformed(t *testing.T) {
	tests := map[string]func(*runnablev0.Operation){
		"no name":                   func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].Name = "" },
		"name beginning with digit": func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].Name = "1model" },
		"name with a dash":          func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].Name = "model-a" },
		"duplicate name":            func(o *runnablev0.Operation) { o.RequiredScopeSlots[1].Name = "model" },
		"wildcard required action":  func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].RequiredActions = []string{"*"} },
		"blank required action":     func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].RequiredActions = []string{" read"} },
		"repeated required action":  func(o *runnablev0.Operation) { o.RequiredScopeSlots[0].RequiredActions = []string{"read", "read"} },
		"more slots than scopes may be bound": func(o *runnablev0.Operation) {
			o.RequiredScopeSlots = nil
			for i := 0; i <= runnable.MaxScopes; i++ {
				o.RequiredScopeSlots = append(o.RequiredScopeSlots, &runnablev0.ScopeSlot{Name: fmt.Sprintf("slot_%d", i)})
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			declared := slottedOperation()
			change(declared)
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			// The same rule holds a delivered policy: a binding is validated as the
			// descriptor would have been.
			binding := connectBinding()
			binding.Policy = declared
			_, err = runnable.EncodePrepared(binding)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}
}

// A spec built directly — not derived, so never parsed and never validated —
// is held to the declaration rules by the resolver itself, before it resolves
// or clears anything: a wildcard in a required action or a duplicate slot must
// not slip through to a scope validation that never saw the slots.
func TestResolveScopeSlotsValidatesTheDeclarationItWasHanded(t *testing.T) {
	direct := func() *runnable.OperationSpec {
		return &runnable.OperationSpec{
			Method:         applyText,
			AttemptTimeout: 10 * time.Second,
			TotalTimeout:   time.Minute,
			MaxAttempts:    1,
			Backoff:        time.Second,
			RetryableCodes: []string{"UNAVAILABLE"},
			Audience:       "documents.ingestion",
			InvokeScopes:   []*basev0.WorkScopeV1{scope("documents", nil, "ingest", "read")},
			LookupScopes:   []*basev0.WorkScopeV1{scope("documents", nil, "read")},
			Completion:     basev0.RunnableExecution_COMPLETION_CALL,
			RequiredScopeSlots: []*runnablev0.ScopeSlot{
				{Name: "model", RequiredActions: []string{"invoke", "read"}, Lookup: true},
				{Name: "tools"},
			},
		}
	}
	good, err := direct().ResolveScopeSlots(slotSelections())
	require.NoError(t, err)
	require.Len(t, good.InvokeScopes, 4)

	// Each case is an invalid declaration that a resolver skipping the
	// receiver's validation would RESOLVE, or refuse for the wrong reason: the
	// selection below is shaped to satisfy every later rule, so only the
	// declaration rule, applied first, can be what refuses it.
	type refusal struct {
		change     func(*runnable.OperationSpec)
		selections []*runnablev0.ScopeSelection
		rule       string
	}
	empty := &runnablev0.ScopeSelection{Slot: "", Invoke: []*basev0.WorkScopeV1{scope("robin/model", []string{"model-a"}, "invoke", "read")}, Lookup: []*basev0.WorkScopeV1{scope("robin/model", []string{"model-a"}, "read")}}
	tests := map[string]refusal{
		"empty slot name answered by an empty name": {
			func(s *runnable.OperationSpec) {
				s.RequiredScopeSlots = []*runnablev0.ScopeSlot{{Name: "", RequiredActions: []string{"invoke", "read"}, Lookup: true}}
			},
			[]*runnablev0.ScopeSelection{empty},
			`declares a scope slot named ""`},
		"duplicate required action": {
			func(s *runnable.OperationSpec) { s.RequiredScopeSlots[0].RequiredActions = []string{"read", "read"} },
			slotSelections(),
			`scope slot "model" requires action "read" twice`},
		"wildcard required action": {
			func(s *runnable.OperationSpec) { s.RequiredScopeSlots[1].RequiredActions = []string{"*"} },
			func() []*runnablev0.ScopeSelection {
				s := slotSelections()
				s[1].Invoke = []*basev0.WorkScopeV1{scope("acme.tool/search", []string{"search-1"}, "*")}
				s[1].Lookup = nil
				return s
			}(),
			`scope slot "tools" requires action "*", which is not a usable value`},
		"duplicate slot names": {
			func(s *runnable.OperationSpec) { s.RequiredScopeSlots[1].Name = "model" },
			slotSelections()[:1],
			`declares scope slot "model" twice`},
		"malformed slot name": {
			func(s *runnable.OperationSpec) { s.RequiredScopeSlots[1].Name = "-tools" },
			func() []*runnablev0.ScopeSelection { s := slotSelections(); s[1].Slot = "-tools"; return s }(),
			`declares a scope slot named "-tools"`},
		"an invalid fixed policy as well": {
			func(s *runnable.OperationSpec) { s.Audience = "" },
			slotSelections(),
			`audience "" is not a trust boundary`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			spec := direct()
			test.change(spec)
			_, err := spec.ResolveScopeSlots(test.selections)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.rule)
			require.Len(t, spec.RequiredScopeSlots, len(spec.RequiredScopeSlots), "a refused resolution leaves the receiver as it was")
		})
	}
	var nilSpec *runnable.OperationSpec
	_, err = nilSpec.ResolveScopeSlots(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestScopeSelectionsAreRefusedWhenTheyDoNotAnswerTheSlots(t *testing.T) {
	spec := slottedSpec(t)
	// Each case names the rule it exercises by that rule's own message: a rule
	// deleted from the resolver must not be able to hide behind the generic
	// policy validation that runs afterwards and refuses the same input with
	// another reason.
	type refusal struct {
		change func([]*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection
		rule   string
	}
	tests := map[string]refusal{
		"unknown slot": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			return append(s, &runnablev0.ScopeSelection{Slot: "other", Invoke: []*basev0.WorkScopeV1{scope("x", []string{"1"}, "do")}})
		}, `selects scope slot "other", which it does not declare`},
		"missing slot":   {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection { return s[:1] }, `declares required scope slot "tools" but no ScopeSelection was supplied for it`},
		"nothing at all": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection { return nil }, `declares required scope slot "model" but no ScopeSelection was supplied for it`},
		"selected twice": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection { return append(s, s[1]) }, `scope slot "tools" is selected twice`},
		"no invoke scope": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke = nil
			s[1].Lookup = nil
			return s
		}, `scope slot "tools" is selected with no invoke scope`},
		"wildcard kind": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceKind = "*"
			return s
		}, `resource kind "*", which is not an exact kind`},
		"glob kind": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceKind = "acme.tool/*"
			return s
		}, `which is not an exact kind`},
		"kind of a fixed scope": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceKind = "documents"
			return s
		}, `selects kind "documents", which a fixed invoke scope already binds`},
		"kind selected twice": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[1].ResourceKind = "acme.tool/search"
			s[1].Lookup = nil
			return s
		}, `scope slot "tools" selects kind "acme.tool/search" twice`},
		"kind in two slots": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceKind = "robin/model"
			return s
		}, `scope slots "model" and "tools" both select kind "robin/model"`},
		"no ids (wildcard)": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceIds = nil
			return s
		}, `names no resource id; a slot is never a wildcard`},
		"wildcard id": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceIds = []string{"*"}
			return s
		}, `resource id "*", which is not an exact id`},
		"glob id": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceIds = []string{"search-*"}
			return s
		}, `which is not an exact id`},
		"blank id": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceIds = []string{" search-1"}
			return s
		}, `which is not an exact id`},
		"repeated id": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].ResourceIds = []string{"search-1", "search-1"}
			return s
		}, `scope slot "tools" invoke scope over kind "acme.tool/search" names resource id "search-1" twice`},
		"no actions": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].Actions = nil
			return s
		}, `names 0 actions; between 1 and`},
		"wildcard action": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].Actions = []string{"*"}
			return s
		}, `names action "*", which is not an exact action`},
		"repeated action": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Invoke[0].Actions = []string{"call", "call"}
			return s
		}, `names action "call" twice`},
		// The lookup is kept so that only the required-action rule can refuse this.
		"missing required action": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Invoke[0].Actions = []string{"read"}
			return s
		}, `selects kind "robin/model" without action "invoke", which the owner requires`},
		"missing required lookup": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection { s[0].Lookup = nil; return s }, `with no lookup scope, which the owner requires`},
		"required lookup narrower": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup[0].ResourceIds = []string{"model-a"}
			return s
		}, `requires its lookup scope over kind "robin/model" to name the same exact ids`},
		"lookup not read-only": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup[0].Actions = []string{"read", "invoke"}
			return s
		}, `lookup scope over kind "robin/model" is not read-only`},
		"lookup action not read": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[0].Lookup[0].Actions = []string{"invoke"}
			return s
		}, `is not read-only`},
		"lookup ids not covered": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Lookup[0].ResourceIds = []string{"cal-9"}
			return s
		}, `names resource id "cal-9", which its invoke scope does not cover`},
		"lookup over an unselected kind": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Lookup[0].ResourceKind = "acme.tool/mail"
			return s
		}, `selects a lookup scope over kind "acme.tool/mail", which none of its invoke scopes names`},
		"lookup over a kind without read": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Lookup = append(s[1].Lookup, scope("acme.tool/search", []string{"search-1"}, "read"))
			return s
		}, `lookup scope over kind "acme.tool/search" is not covered: its invoke scope does not grant "read"`},
		"two lookups over one kind": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Lookup = append(s[1].Lookup, scope("acme.tool/calendar", []string{"cal-2"}, "read"))
			return s
		}, `selects two lookup scopes over kind "acme.tool/calendar"`},
		"lookup with no ids": {func(s []*runnablev0.ScopeSelection) []*runnablev0.ScopeSelection {
			s[1].Lookup[0].ResourceIds = nil
			return s
		}, `lookup scope over kind "acme.tool/calendar" names no resource id`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := spec.ResolveScopeSlots(test.change(slotSelections()))
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.rule)
		})
	}
}
