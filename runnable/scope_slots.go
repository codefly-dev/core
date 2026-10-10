package runnable

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// MaxScopeSlotNameLength bounds a slot's name.
const MaxScopeSlotNameLength = 64

// ErrUnresolvedScopeSlots marks a policy that still carries a required scope
// slot: an authority with a hole in it, which no binding may deliver and no
// installation may admit. It is always wrapped together with ErrInvalid.
var ErrUnresolvedScopeSlots = errors.New("required scope slots are unresolved")

// validateScopeSlots applies the declaration rules to an owner's slots. An
// owner names a slot and states what it needs of whatever fills it — actions
// every selected invoke scope must carry, whether a read-only lookup must come
// with it — and nothing else: the resource kinds, actions and ids are the
// composition's, which is the whole reason the slot exists.
func validateScopeSlots(subject string, slots []*runnablev0.ScopeSlot) error {
	if len(slots) > MaxScopes {
		return fmt.Errorf("%w: %s declares %d scope slots; at most %d scopes may be bound", ErrInvalid, subject, len(slots), MaxScopes)
	}
	names := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		name := slot.GetName()
		if !validScopeSlotName(name) {
			return fmt.Errorf("%w: %s declares a scope slot named %q; a slot name is 1..%d ASCII letters, digits or '_' and begins with a letter",
				ErrInvalid, subject, name, MaxScopeSlotNameLength)
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("%w: %s declares scope slot %q twice", ErrInvalid, subject, name)
		}
		names[name] = struct{}{}
		required := slot.GetRequiredActions()
		if len(required) > MaxScopeActions {
			return fmt.Errorf("%w: %s scope slot %q requires %d actions; at most %d may be required", ErrInvalid, subject, name, len(required), MaxScopeActions)
		}
		seen := make(map[string]struct{}, len(required))
		for _, action := range required {
			if !boundedScopeValue(action, MaxScopeActionLength) || strings.Contains(action, "*") {
				return fmt.Errorf("%w: %s scope slot %q requires action %q, which is not a usable value; a slot is never a wildcard", ErrInvalid, subject, name, action)
			}
			if _, exists := seen[action]; exists {
				return fmt.Errorf("%w: %s scope slot %q requires action %q twice", ErrInvalid, subject, name, action)
			}
			seen[action] = struct{}{}
		}
	}
	return nil
}

func validScopeSlotName(name string) bool {
	if len(name) == 0 || len(name) > MaxScopeSlotNameLength {
		return false
	}
	for i, c := range []byte(name) {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '_')) {
			return false
		}
	}
	return true
}

// ResolveScopeSlots is the one resolution of an owner's required scope slots
// to a concrete policy. A composition answers each slot with the exact scopes
// it forwards into it — resource kinds, actions and explicit ids all its own —
// and the result is the spec with those invoke scopes, and the read-only
// lookup scopes that come with them, appended to the owner's fixed scopes, no
// slots left, validated as any policy is.
//
// It refuses a selection naming no declared slot, a slot left unselected, a
// slot selected twice, a selection with no invoke scope, and a kind, action or
// id that is empty, blank or a wildcard; a selected kind that a fixed invoke
// scope or another slot already binds; an invoke scope missing an action the
// owner requires; a lookup scope that is not the one read-only action, names
// a kind no invoke scope of the selection names, or names ids its invoke scope
// does not cover; and, where the slot asks for lookup, an invoke scope with no
// lookup scope over the same exact ids. A slot is required and exact, never a
// default and never a wildcard.
//
// The receiver is validated as declared before anything is resolved: a spec
// built directly rather than derived may carry a slot the declaration rules
// refuse, and clearing the slots first would hide it behind an ordinary scope
// validation that never saw it. The call is pure — receiver and selections are
// read and never written, and the result shares no memory with either — so a
// caller resolves once and hands the same concrete policy to every place the
// policy appears.
func (s *OperationSpec) ResolveScopeSlots(selections []*runnablev0.ScopeSelection) (*OperationSpec, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: an operation spec is required to resolve scope slots", ErrInvalid)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	declared := make(map[string]*runnablev0.ScopeSlot, len(s.RequiredScopeSlots))
	for _, slot := range s.RequiredScopeSlots {
		declared[slot.GetName()] = slot
	}
	bySlot := make(map[string]*runnablev0.ScopeSelection, len(selections))
	for _, selection := range selections {
		name := selection.GetSlot()
		if _, known := declared[name]; !known {
			return nil, fmt.Errorf("%w: %s selects scope slot %q, which it does not declare", ErrInvalid, s.Method, name)
		}
		if _, twice := bySlot[name]; twice {
			return nil, fmt.Errorf("%w: %s scope slot %q is selected twice; one selection answers one slot", ErrInvalid, s.Method, name)
		}
		bySlot[name] = selection
	}
	fixed := make(map[string]struct{}, len(s.InvokeScopes))
	for _, scope := range s.InvokeScopes {
		fixed[scope.GetResourceKind()] = struct{}{}
	}
	selected := make(map[string]string)
	resolved := s.clone()
	resolved.RequiredScopeSlots = nil
	// Declaration order, then selection order: the concrete policy is a
	// function of the declaration and the selection and of nothing else, so
	// what an installation digests is reproducible.
	for _, slot := range s.RequiredScopeSlots {
		name := slot.GetName()
		selection, ok := bySlot[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s declares required scope slot %q but no ScopeSelection was supplied for it; the composition must select exact authority for this slot", ErrInvalid, s.Method, name)
		}
		if len(selection.GetInvoke()) == 0 {
			return nil, fmt.Errorf("%w: %s scope slot %q is selected with no invoke scope; a slot is required and never a wildcard", ErrInvalid, s.Method, name)
		}
		lookups := make(map[string]*basev0.WorkScopeV1, len(selection.GetLookup()))
		for _, lookup := range selection.GetLookup() {
			kind := lookup.GetResourceKind()
			if _, twice := lookups[kind]; twice {
				return nil, fmt.Errorf("%w: %s scope slot %q selects two lookup scopes over kind %q", ErrInvalid, s.Method, name, kind)
			}
			lookups[kind] = lookup
		}
		for _, scope := range selection.GetInvoke() {
			kind := scope.GetResourceKind()
			if err := s.validateSelectedScope(name, "invoke", scope); err != nil {
				return nil, err
			}
			if _, owned := fixed[kind]; owned {
				return nil, fmt.Errorf("%w: %s scope slot %q selects kind %q, which a fixed invoke scope already binds; a kind is the owner's or the composition's, never both",
					ErrInvalid, s.Method, name, kind)
			}
			if other, taken := selected[kind]; taken {
				if other == name {
					return nil, fmt.Errorf("%w: %s scope slot %q selects kind %q twice", ErrInvalid, s.Method, name, kind)
				}
				return nil, fmt.Errorf("%w: %s scope slots %q and %q both select kind %q", ErrInvalid, s.Method, other, name, kind)
			}
			selected[kind] = name
			for _, required := range slot.GetRequiredActions() {
				if !slices.Contains(scope.GetActions(), required) {
					return nil, fmt.Errorf("%w: %s scope slot %q selects kind %q without action %q, which the owner requires", ErrInvalid, s.Method, name, kind, required)
				}
			}
			resolved.InvokeScopes = append(resolved.InvokeScopes, proto.CloneOf(scope))
			lookup, has := lookups[kind]
			if !has {
				if slot.GetLookup() {
					return nil, fmt.Errorf("%w: %s scope slot %q selects kind %q with no lookup scope, which the owner requires to read receipts back", ErrInvalid, s.Method, name, kind)
				}
				continue
			}
			delete(lookups, kind)
			if err := s.validateSelectedScope(name, "lookup", lookup); err != nil {
				return nil, err
			}
			if actions := lookup.GetActions(); len(actions) != 1 || actions[0] != ReadOnlyScopeAction {
				return nil, fmt.Errorf("%w: %s scope slot %q lookup scope over kind %q is not read-only: a receipt may only be %q", ErrInvalid, s.Method, name, kind, ReadOnlyScopeAction)
			}
			if !slices.Contains(scope.GetActions(), ReadOnlyScopeAction) {
				return nil, fmt.Errorf("%w: %s scope slot %q lookup scope over kind %q is not covered: its invoke scope does not grant %q", ErrInvalid, s.Method, name, kind, ReadOnlyScopeAction)
			}
			for _, id := range lookup.GetResourceIds() {
				if !slices.Contains(scope.GetResourceIds(), id) {
					return nil, fmt.Errorf("%w: %s scope slot %q lookup scope over kind %q names resource id %q, which its invoke scope does not cover", ErrInvalid, s.Method, name, kind, id)
				}
			}
			if slot.GetLookup() && len(lookup.GetResourceIds()) != len(scope.GetResourceIds()) {
				return nil, fmt.Errorf("%w: %s scope slot %q requires its lookup scope over kind %q to name the same exact ids as its invoke scope", ErrInvalid, s.Method, name, kind)
			}
			resolved.LookupScopes = append(resolved.LookupScopes, proto.CloneOf(lookup))
		}
		if len(lookups) > 0 {
			kinds := slices.Sorted(mapKeys(lookups))
			return nil, fmt.Errorf("%w: %s scope slot %q selects a lookup scope over kind %q, which none of its invoke scopes names", ErrInvalid, s.Method, name, kinds[0])
		}
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// validateSelectedScope holds one selected scope to what a selection may say:
// an exact kind, exact actions and exact ids, each bounded as the runtime
// bounds a scope and none of them a wildcard — including the wildcard by
// omission, an empty resource_ids that would be every resource of the kind.
func (s *OperationSpec) validateSelectedScope(slot, at string, scope *basev0.WorkScopeV1) error {
	kind := scope.GetResourceKind()
	if !boundedScopeValue(kind, MaxScopeKindLength) || strings.Contains(kind, "*") {
		return fmt.Errorf("%w: %s scope slot %q selects an %s scope with resource kind %q, which is not an exact kind", ErrInvalid, s.Method, slot, at, kind)
	}
	actions := scope.GetActions()
	if len(actions) == 0 || len(actions) > MaxScopeActions {
		return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names %d actions; between 1 and %d are required", ErrInvalid, s.Method, slot, at, kind, len(actions), MaxScopeActions)
	}
	seen := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		if !boundedScopeValue(action, MaxScopeActionLength) || strings.Contains(action, "*") {
			return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names action %q, which is not an exact action", ErrInvalid, s.Method, slot, at, kind, action)
		}
		if _, exists := seen[action]; exists {
			return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names action %q twice", ErrInvalid, s.Method, slot, at, kind, action)
		}
		seen[action] = struct{}{}
	}
	ids := scope.GetResourceIds()
	if len(ids) == 0 {
		return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names no resource id; a slot is never a wildcard", ErrInvalid, s.Method, slot, at, kind)
	}
	if len(ids) > MaxScopeResourceIds {
		return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names %d resource ids; at most %d may be selected", ErrInvalid, s.Method, slot, at, kind, len(ids), MaxScopeResourceIds)
	}
	seenIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !boundedScopeValue(id, MaxScopeResourceIdLength) || strings.Contains(id, "*") {
			return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names resource id %q, which is not an exact id", ErrInvalid, s.Method, slot, at, kind, id)
		}
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("%w: %s scope slot %q %s scope over kind %q names resource id %q twice", ErrInvalid, s.Method, slot, at, kind, id)
		}
		seenIDs[id] = struct{}{}
	}
	return nil
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// clone is a deep copy: a derived spec is handed to generators and resolvers
// that own what they hold, and must not be able to write through to the one
// they were handed.
func (s *OperationSpec) clone() *OperationSpec {
	out := *s
	out.RetryableCodes = slices.Clone(s.RetryableCodes)
	out.InvokeScopes = clonedScopes(s.InvokeScopes)
	out.LookupScopes = clonedScopes(s.LookupScopes)
	out.Tool = proto.CloneOf(s.Tool)
	out.RequiredScopeSlots = clonedSlots(s.RequiredScopeSlots)
	return &out
}

// clonedSlots copies an option's slots out of the descriptor, as clonedScopes
// does for scopes: a descriptor's options are shared and must not be written
// through.
func clonedSlots(slots []*runnablev0.ScopeSlot) []*runnablev0.ScopeSlot {
	if slots == nil {
		return nil
	}
	cloned := make([]*runnablev0.ScopeSlot, 0, len(slots))
	for _, slot := range slots {
		cloned = append(cloned, proto.CloneOf(slot))
	}
	return cloned
}
