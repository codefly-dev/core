package workcontext

import (
	"context"
	"errors"
	"fmt"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ErrUnsealed is returned when a capability carries no seal, or carries one
// that names no installation. It is distinct from ErrInvalid because the two
// name different situations: an invalid capability is one that was malformed or
// forged, and an unsealed one is a capability from before the seal existed, or
// one minted by a component that has not adopted it.
//
// A capability with no seal does not verify. The schema does not require the
// field — a schema rule would retroactively invalidate every archived
// capability and every execution receipt embedding one — so the requirement
// lives here, in the one place that decides whether a token is a credential.
var ErrUnsealed = errors.New("work context: carries no seal")

// Seal is the live binding of a principal's authority to one installation and
// one execution, as the issuer holds it right now. A Verifier compares the
// capability's sealed values against this field by field, for exact equality.
//
// Exact equality, not "at least": a capability carrying a revision HIGHER than
// the issuer's live one is refused too. There is no legitimate way to hold one
// — it would mean a capability sealed to an installation state that has not
// happened — so the shapes that could produce it are a rolled-back
// installation and a forged seal, and neither is a thing to accept.
type Seal struct {
	// PrincipalEpoch is the principal's current epoch. Advancing it
	// invalidates every capability minted for that principal at once,
	// without waiting for any of them to expire.
	PrincipalEpoch uint64

	// InstallationID is the installation the authority is held through.
	InstallationID string

	// InstallationRevision is the installation's current revision. The scope
	// of an installation changes with its revision, so a capability sealed to
	// an earlier one is asking for authority under terms that no longer apply.
	InstallationRevision uint64

	// BuildIncarnation is the current incarnation of the build. It separates
	// two runs of one approved build, so a capability cannot be carried from a
	// replaced incarnation into a new one.
	BuildIncarnation uint64
}

// OperationBinding is the live state of one unit of authority, resolved by its
// opaque ID. Revision changes when the binding's terms change; Incarnation
// changes when the binding is re-established rather than merely revised, so a
// capability sealed to a binding that was withdrawn and re-created does not
// verify against the new one.
type OperationBinding struct {
	// ID is the binding's opaque identifier, echoed back so a source that
	// resolves an alias cannot silently answer for a different binding.
	ID string

	// Revision is the binding's current revision.
	Revision uint64

	// Incarnation is the binding's current incarnation.
	Incarnation uint64

	// Revoked marks the binding withdrawn. A revoked binding refuses every
	// capability sealed to it, whatever its revision.
	Revoked bool
}

// ErrNoSeal is what a SealSource returns when the principal holds no such
// installation. A verifier turns it into a refusal: a capability sealed to an
// installation its principal does not hold is not a credential, and the lookup
// coming back empty is the answer rather than an error on the way to one.
var ErrNoSeal = errors.New("work context: principal holds no such installation")

// ErrNoBinding is what a SealSource returns when no binding exists under the
// ID a capability sealed. A verifier turns it into a refusal rather than an
// outage: a capability naming a binding that does not exist is not a
// credential, and the lookup failing is the answer rather than an error on the
// way to one.
var ErrNoBinding = errors.New("work context: no such operation binding")

// SealSource answers the live values a seal is compared against.
//
// There is deliberately no method that lists bindings or finds one matching a
// set of scopes. The verification contract is an exact lookup by the sealed ID,
// and "search the bindings for one that contains these scopes" is a predicate
// somebody writes — a predicate one case too generous grants authority nobody
// reviewed, and it fails in the direction of granting rather than refusing.
// Making the search unexpressible through this interface is what keeps it out.
type SealSource interface {
	// Seal answers the live seal for one principal's use of one installation.
	// The installation is named by the caller because identity is the caller's
	// to supply; the counters are the issuer's to answer, which is why a minter
	// reads them here instead of accepting them as input.
	Seal(ctx context.Context, principalID, installationID string) (Seal, error)

	// OperationBinding resolves one binding by its opaque ID, exactly. It
	// returns ErrNoBinding when there is none.
	OperationBinding(ctx context.Context, bindingID string) (OperationBinding, error)
}

// sealOf reads the capability's seal, refusing a capability that carries none.
func sealOf(wc *basev0.WorkContextV1) (*basev0.WorkSealV1, error) {
	seal := wc.GetSeal()
	if seal == nil {
		return nil, fmt.Errorf("%w: principal %q session %q", ErrUnsealed, wc.GetOwnerPrincipalId(), wc.GetSessionId())
	}
	if seal.GetInstallationId() == "" {
		return nil, fmt.Errorf("%w: the seal names no installation", ErrUnsealed)
	}
	return seal, nil
}

// checkSeal holds a capability's seal against the issuer's live values, and its
// operation binding against the one binding its sealed ID resolves to.
//
// Every mismatch is ErrRevoked rather than ErrInvalid. The capability is not
// malformed and was not forged: it was sound when it was minted and the state
// it was sealed to has moved, which is the same situation a superseded
// authorization revision describes, and a caller distinguishing "re-mint" from
// "reject this caller" needs them to read alike.
func (v *Verifier) checkSeal(ctx context.Context, wc *basev0.WorkContextV1) error {
	sealed, err := sealOf(wc)
	if err != nil {
		return err
	}
	// The principal the seal is held for is the task's owner, not the current
	// actor: the installation is the owner's, and a delegation hop does not
	// move it. Reading the actor here would look up an installation the actor
	// may hold under a different revision and compare the wrong two numbers.
	principal := wc.GetOwnerPrincipalId()
	live, err := v.Seals.Seal(ctx, principal, sealed.GetInstallationId())
	if err != nil {
		if errors.Is(err, ErrNoSeal) {
			return fmt.Errorf("%w: sealed to installation %q, which principal %q does not hold", ErrRevoked, sealed.GetInstallationId(), principal)
		}
		return fmt.Errorf("work context: seal for principal %q installation %q: %w", principal, sealed.GetInstallationId(), err)
	}
	// A source that answered for a different installation than the one asked
	// about would make every comparison below meaningless, so it is checked
	// rather than assumed.
	if live.InstallationID != sealed.GetInstallationId() {
		return fmt.Errorf("work context: seal source answered for installation %q, not %q", live.InstallationID, sealed.GetInstallationId())
	}
	for _, field := range []struct {
		label  string
		sealed uint64
		live   uint64
	}{
		{"principal epoch", sealed.GetPrincipalEpoch(), live.PrincipalEpoch},
		{"installation revision", sealed.GetInstallationRevision(), live.InstallationRevision},
		{"build incarnation", sealed.GetBuildIncarnation(), live.BuildIncarnation},
	} {
		if field.sealed != field.live {
			return fmt.Errorf("%w: sealed to %s %d, the issuer holds %d", ErrRevoked, field.label, field.sealed, field.live)
		}
	}
	binding := wc.GetOperationBinding()
	if binding == nil {
		return nil
	}
	// Exact lookup by the sealed ID. Nothing here chooses a binding; the ID
	// chose it when the capability was minted, and this either finds that one
	// binding or refuses.
	resolved, err := v.Seals.OperationBinding(ctx, binding.GetBindingId())
	if err != nil {
		if errors.Is(err, ErrNoBinding) {
			return fmt.Errorf("%w: sealed to binding %q, which the issuer does not hold", ErrRevoked, binding.GetBindingId())
		}
		return fmt.Errorf("work context: operation binding %q: %w", binding.GetBindingId(), err)
	}
	if resolved.ID != binding.GetBindingId() {
		return fmt.Errorf("work context: seal source answered for binding %q, not %q", resolved.ID, binding.GetBindingId())
	}
	if resolved.Revoked {
		return fmt.Errorf("%w: binding %q is revoked", ErrRevoked, binding.GetBindingId())
	}
	for _, field := range []struct {
		label  string
		sealed uint64
		live   uint64
	}{
		{"revision", binding.GetRevision(), resolved.Revision},
		{"incarnation", binding.GetIncarnation(), resolved.Incarnation},
	} {
		if field.sealed != field.live {
			return fmt.Errorf("%w: sealed to binding %q %s %d, the issuer holds %d",
				ErrRevoked, binding.GetBindingId(), field.label, field.sealed, field.live)
		}
	}
	return nil
}

// sealFor reads the live seal and, when the caller named an operation binding,
// the live binding, and returns the messages a capability carries. A minter
// reads them rather than accepting them so that a capability can never be
// sealed to a state the issuer is not actually in: a minter that took the
// numbers as input would hand out capabilities nothing verifies, and the
// failure would surface in another process as an authentication error.
func (a *Authority) sealFor(ctx context.Context, principalID, installationID, bindingID string) (*basev0.WorkSealV1, *basev0.WorkOperationBindingV1, error) {
	if a.Seals == nil {
		return nil, nil, fmt.Errorf("work context: authority has no seal source")
	}
	if installationID == "" {
		return nil, nil, fmt.Errorf("%w: a capability is sealed to an installation, so one must be named", ErrInvalid)
	}
	live, err := a.Seals.Seal(ctx, principalID, installationID)
	if err != nil {
		return nil, nil, fmt.Errorf("work context: seal for principal %q installation %q: %w", principalID, installationID, err)
	}
	if live.InstallationID != installationID {
		return nil, nil, fmt.Errorf("work context: seal source answered for installation %q, not %q", live.InstallationID, installationID)
	}
	seal := &basev0.WorkSealV1{
		PrincipalEpoch:       live.PrincipalEpoch,
		InstallationId:       live.InstallationID,
		InstallationRevision: live.InstallationRevision,
		BuildIncarnation:     live.BuildIncarnation,
	}
	if bindingID == "" {
		return seal, nil, nil
	}
	resolved, err := a.Seals.OperationBinding(ctx, bindingID)
	if err != nil {
		return nil, nil, fmt.Errorf("work context: operation binding %q: %w", bindingID, err)
	}
	if resolved.ID != bindingID {
		return nil, nil, fmt.Errorf("work context: seal source answered for binding %q, not %q", resolved.ID, bindingID)
	}
	if resolved.Revoked {
		return nil, nil, fmt.Errorf("%w: binding %q is revoked", ErrInvalid, bindingID)
	}
	return seal, &basev0.WorkOperationBindingV1{
		BindingId:   resolved.ID,
		Revision:    resolved.Revision,
		Incarnation: resolved.Incarnation,
	}, nil
}

// MemorySealSource is an in-process SealSource. It is the real implementation
// for a single issuer and for tests; a deployment whose installations change
// outside this process needs one backed by that store.
//
// It is keyed by (principal, installation) because that is what a seal is held
// for: one principal's use of one installation. A principal with two
// installations has two seals, and conflating them would compare the revision
// of one against the other.
type MemorySealSource struct {
	mu       sync.RWMutex
	seals    map[string]Seal
	bindings map[string]OperationBinding
}

// NewMemorySealSource returns an empty source.
func NewMemorySealSource() *MemorySealSource {
	return &MemorySealSource{seals: map[string]Seal{}, bindings: map[string]OperationBinding{}}
}

func sealKey(principalID, installationID string) string {
	return principalID + "\x00" + installationID
}

// Put records the live seal for one principal's use of one installation. The
// seal's own InstallationID must be the one it is recorded under, so a source
// cannot be loaded with a seal that answers for a different installation than
// it was asked about.
func (s *MemorySealSource) Put(principalID string, seal Seal) error {
	if principalID == "" || seal.InstallationID == "" {
		return fmt.Errorf("%w: a seal is held for one principal's use of one installation", ErrInvalid)
	}
	if seal.PrincipalEpoch == 0 || seal.InstallationRevision == 0 || seal.BuildIncarnation == 0 {
		// Zero is not a revision, and a capability sealed to one would compare
		// equal to a source that simply had nothing recorded.
		return fmt.Errorf("%w: a seal's epoch, revision and incarnation all start at 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seals[sealKey(principalID, seal.InstallationID)] = seal
	return nil
}

// PutBinding records the live state of one operation binding.
func (s *MemorySealSource) PutBinding(binding OperationBinding) error {
	if binding.ID == "" {
		return fmt.Errorf("%w: a binding needs an ID", ErrInvalid)
	}
	if binding.Revision == 0 || binding.Incarnation == 0 {
		return fmt.Errorf("%w: a binding's revision and incarnation both start at 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[binding.ID] = binding
	return nil
}

// Seal implements SealSource.
func (s *MemorySealSource) Seal(_ context.Context, principalID, installationID string) (Seal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seal, held := s.seals[sealKey(principalID, installationID)]
	if !held {
		return Seal{}, fmt.Errorf("%w: principal %q, installation %q", ErrNoSeal, principalID, installationID)
	}
	return seal, nil
}

// OperationBinding implements SealSource. A binding that is not held is
// ErrNoBinding, which a verifier turns into a refusal rather than an outage.
func (s *MemorySealSource) OperationBinding(_ context.Context, bindingID string) (OperationBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, held := s.bindings[bindingID]
	if !held {
		return OperationBinding{}, fmt.Errorf("%w: %q", ErrNoBinding, bindingID)
	}
	return binding, nil
}
