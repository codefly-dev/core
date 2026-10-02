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

	// PrincipalID is the principal this binding is GRANTED TO, and
	// InstallationID the installation it is granted within. Both are required.
	//
	// They are the difference between a binding existing and a binding being
	// held. An ID alone is not authorization: without the association, a
	// capability sealed to one principal's installation could name a binding
	// granted to a different principal in a different installation, and a
	// verifier that looked the ID up and compared only its counters would
	// accept it. So the verifier requires that the resolved binding is granted
	// to the principal exercising the capability, within the installation the
	// capability is sealed to.
	PrincipalID    string
	InstallationID string

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

	// PrincipalEpoch answers the live epoch of one principal, independent of
	// any installation.
	//
	// It is separate from Seal because an ACTOR's authority is narrowed
	// independently of the owner's: a delegated operation context may have a
	// person as its owner and a service principal as its actor, and narrowing
	// that service principal has to reach every capability it acts in. The
	// seal's epoch is the owner's and cannot answer for the actor. It returns
	// ErrNoSeal when the principal is not known.
	PrincipalEpoch(ctx context.Context, principalID string) (uint64, error)

	// OperationBinding resolves one binding by its opaque ID, exactly. It
	// returns ErrNoBinding when there is none.
	OperationBinding(ctx context.Context, bindingID string) (OperationBinding, error)
}

// exercisingPrincipal is the principal whose authority a capability spends:
// the last actor hop, or the owner when the owner acts directly. It is the
// principal an operation binding must be granted to, because it is the one
// making the call.
func exercisingPrincipal(wc *basev0.WorkContextV1) string {
	chain := wc.GetActorChain()
	if len(chain) == 0 {
		return wc.GetOwnerPrincipalId()
	}
	return chain[len(chain)-1].GetPrincipalId()
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
	// The principal the seal is held for is the task's OWNER: the installation
	// is the owner's, and a delegation hop narrows authority within it rather
	// than moving it. Each actor's own epoch is checked separately below,
	// because an actor is narrowed independently of the owner.
	owner := wc.GetOwnerPrincipalId()
	live, err := v.Seals.Seal(ctx, owner, sealed.GetInstallationId())
	if err != nil {
		if errors.Is(err, ErrNoSeal) {
			return fmt.Errorf("%w: sealed to installation %q, which principal %q does not hold", ErrRevoked, sealed.GetInstallationId(), owner)
		}
		return fmt.Errorf("work context: seal for principal %q installation %q: %w", owner, sealed.GetInstallationId(), err)
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
	if err := v.checkActorEpochs(ctx, wc); err != nil {
		return err
	}
	return v.checkOperationBinding(ctx, wc, sealed)
}

// checkActorEpochs holds every actor hop's sealed epoch against that
// principal's live epoch.
//
// Without this, revoking a delegated actor does nothing to the capabilities it
// acts in: the seal's epoch is the OWNER's, so advancing the actor's epoch
// leaves a capability whose owner is unchanged fully valid until it expires.
// The only lever left would be the tenant's authorization revision, which cuts
// off every capability of the tenant rather than the one compromised
// principal.
//
// Every hop is checked, and the check does not branch on the principal's kind.
// A rule that applied only to some kinds of principal would be an
// authorization decision made from a kind field, and it would leave whichever
// kinds it skipped unrevocable.
func (v *Verifier) checkActorEpochs(ctx context.Context, wc *basev0.WorkContextV1) error {
	for index, hop := range wc.GetActorChain() {
		// A hop carrying no epoch is not revocable, so it is refused. The
		// schema cannot require the field without invalidating every archived
		// capability, which is why the requirement lives here.
		if hop.PrincipalEpoch == nil {
			return fmt.Errorf("%w: actor hop %d (%s) carries no epoch, so it cannot be revoked", ErrUnsealed, index, hop.GetPrincipalId())
		}
		current, err := v.Seals.PrincipalEpoch(ctx, hop.GetPrincipalId())
		if err != nil {
			if errors.Is(err, ErrNoSeal) {
				return fmt.Errorf("%w: actor hop %d names principal %q, which the issuer does not hold", ErrRevoked, index, hop.GetPrincipalId())
			}
			return fmt.Errorf("work context: epoch for principal %q: %w", hop.GetPrincipalId(), err)
		}
		if hop.GetPrincipalEpoch() != current {
			return fmt.Errorf("%w: actor hop %d (%s) is sealed to epoch %d, the issuer holds %d",
				ErrRevoked, index, hop.GetPrincipalId(), hop.GetPrincipalEpoch(), current)
		}
	}
	return nil
}

// checkOperationBinding resolves the sealed binding by exact ID and requires
// that it is GRANTED TO the principal exercising the capability, within the
// installation the capability is sealed to.
//
// The association is the point. Resolving the ID and comparing only its
// counters would accept a capability sealed to one principal's installation
// that names a binding granted to another principal in another installation:
// the ID exists, the counters match, and nothing has asked whether the caller
// holds it. An opaque ID is not authorization.
func (v *Verifier) checkOperationBinding(ctx context.Context, wc *basev0.WorkContextV1, sealed *basev0.WorkSealV1) error {
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
	if resolved.InstallationID != sealed.GetInstallationId() {
		return fmt.Errorf("%w: binding %q is granted within installation %q and this capability is sealed to %q",
			ErrRevoked, binding.GetBindingId(), resolved.InstallationID, sealed.GetInstallationId())
	}
	if exercising := exercisingPrincipal(wc); resolved.PrincipalID != exercising {
		return fmt.Errorf("%w: binding %q is granted to principal %q and this capability is exercised by %q",
			ErrRevoked, binding.GetBindingId(), resolved.PrincipalID, exercising)
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
//
// exercising is the principal that will spend the binding — the hop being
// added, or the owner when the owner acts directly. The binding must be
// granted to it, within this installation, or the mint is refused: minting a
// capability the verifier will reject is a failure best raised here.
func (a *Authority) sealFor(ctx context.Context, principalID, installationID, bindingID, exercising string) (*basev0.WorkSealV1, *basev0.WorkOperationBindingV1, error) {
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
	// The same association the verifier requires, enforced at the mint. Any
	// live binding ID would otherwise be mintable into any capability, and the
	// refusal would land on the caller of the operation rather than on the
	// mint that had no business issuing it.
	if resolved.InstallationID != installationID {
		return nil, nil, fmt.Errorf("%w: binding %q is granted within installation %q, not %q",
			ErrInvalid, bindingID, resolved.InstallationID, installationID)
	}
	if resolved.PrincipalID != exercising {
		return nil, nil, fmt.Errorf("%w: binding %q is granted to principal %q, which is not %q",
			ErrInvalid, bindingID, resolved.PrincipalID, exercising)
	}
	return seal, &basev0.WorkOperationBindingV1{
		BindingId:   resolved.ID,
		Revision:    resolved.Revision,
		Incarnation: resolved.Incarnation,
	}, nil
}

// epochFor reads one principal's live epoch, for the hop being added.
func (a *Authority) epochFor(ctx context.Context, principalID string) (uint64, error) {
	if a.Seals == nil {
		return 0, fmt.Errorf("work context: authority has no seal source")
	}
	epoch, err := a.Seals.PrincipalEpoch(ctx, principalID)
	if err != nil {
		return 0, fmt.Errorf("work context: epoch for principal %q: %w", principalID, err)
	}
	if epoch == 0 {
		return 0, fmt.Errorf("work context: principal %q has epoch 0, which names nothing", principalID)
	}
	return epoch, nil
}

// carryForwardSeal holds a derived capability's INHERITED seal against the
// issuer's live state and refuses the derivation when any of it has moved. The
// inherited values are kept; nothing is overwritten.
//
// This is the opposite of what the authorization revision does, and the
// difference is the whole point. A revision is monotonic and forward-only, so
// re-reading it avoids minting a child born superseded. A SEAL is compared for
// equality and a change to it is a REVOCATION — so a parent whose sealed state
// has moved is a dead credential, and anything derived from it must be refused
// rather than silently stamped with the current numbers.
//
// Overwriting was the bug: verify a parent at installation revision 3, advance
// the installation to 4, and a derived child kept the parent's authority while
// carrying revision 4 and verifying. The revocation this package introduces
// would then have been defeated by derivation.
func (a *Authority) carryForwardSeal(ctx context.Context, wc *basev0.WorkContextV1) error {
	inherited, err := sealOf(wc)
	if err != nil {
		return err
	}
	if a.Seals == nil {
		return fmt.Errorf("work context: authority has no seal source")
	}
	owner := wc.GetOwnerPrincipalId()
	live, err := a.Seals.Seal(ctx, owner, inherited.GetInstallationId())
	if err != nil {
		if errors.Is(err, ErrNoSeal) {
			return fmt.Errorf("%w: the parent is sealed to installation %q, which principal %q no longer holds",
				ErrRevoked, inherited.GetInstallationId(), owner)
		}
		return fmt.Errorf("work context: seal for principal %q installation %q: %w", owner, inherited.GetInstallationId(), err)
	}
	for _, field := range []struct {
		label     string
		inherited uint64
		live      uint64
	}{
		{"principal epoch", inherited.GetPrincipalEpoch(), live.PrincipalEpoch},
		{"installation revision", inherited.GetInstallationRevision(), live.InstallationRevision},
		{"build incarnation", inherited.GetBuildIncarnation(), live.BuildIncarnation},
	} {
		if field.inherited != field.live {
			return fmt.Errorf("%w: the parent is sealed to %s %d and the issuer holds %d, so it can derive nothing; mint afresh",
				ErrRevoked, field.label, field.inherited, field.live)
		}
	}
	// The binding the parent already carries is held to the same standard. A
	// hop that names a NEW binding replaces it, and that replacement is
	// resolved and entitlement-checked by sealFor rather than carried.
	if carried := wc.GetOperationBinding(); carried != nil {
		resolved, err := a.Seals.OperationBinding(ctx, carried.GetBindingId())
		if err != nil {
			if errors.Is(err, ErrNoBinding) {
				return fmt.Errorf("%w: the parent is sealed to binding %q, which the issuer does not hold", ErrRevoked, carried.GetBindingId())
			}
			return fmt.Errorf("work context: operation binding %q: %w", carried.GetBindingId(), err)
		}
		if resolved.Revoked {
			return fmt.Errorf("%w: the parent's binding %q is revoked", ErrRevoked, carried.GetBindingId())
		}
		if resolved.Revision != carried.GetRevision() || resolved.Incarnation != carried.GetIncarnation() {
			return fmt.Errorf("%w: the parent is sealed to binding %q revision %d incarnation %d and the issuer holds %d/%d",
				ErrRevoked, carried.GetBindingId(), carried.GetRevision(), carried.GetIncarnation(), resolved.Revision, resolved.Incarnation)
		}
	}
	return nil
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
	epochs   map[string]uint64
	bindings map[string]OperationBinding
}

// NewMemorySealSource returns an empty source.
func NewMemorySealSource() *MemorySealSource {
	return &MemorySealSource{
		seals:    map[string]Seal{},
		epochs:   map[string]uint64{},
		bindings: map[string]OperationBinding{},
	}
}

func sealKey(principalID, installationID string) string {
	return principalID + "\x00" + installationID
}

// PutEpoch records one principal's live epoch, independent of any
// installation. Put also records the owner's epoch as a side effect of
// recording its seal; this is for principals that are only ever actors.
func (s *MemorySealSource) PutEpoch(principalID string, epoch uint64) error {
	if principalID == "" || epoch == 0 {
		return fmt.Errorf("%w: a principal's epoch starts at 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epochs[principalID] = epoch
	return nil
}

// PrincipalEpoch implements SealSource.
func (s *MemorySealSource) PrincipalEpoch(_ context.Context, principalID string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	epoch, held := s.epochs[principalID]
	if !held {
		return 0, fmt.Errorf("%w: principal %q", ErrNoSeal, principalID)
	}
	return epoch, nil
}

// Put records the live seal for one principal's use of one installation. The
// seal's own InstallationID must be the one it is recorded under, so a source
// cannot be loaded with a seal that answers for a different installation than
// it was asked about. It also records that principal's epoch, so an owner is
// answerable as an actor without a second call.
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
	s.epochs[principalID] = seal.PrincipalEpoch
	return nil
}

// PutBinding records the live state of one operation binding.
func (s *MemorySealSource) PutBinding(binding OperationBinding) error {
	if binding.ID == "" {
		return fmt.Errorf("%w: a binding needs an ID", ErrInvalid)
	}
	// The association is required, because a binding without one is a binding
	// nothing can be entitled to — and the verifier would refuse it anyway.
	if binding.PrincipalID == "" || binding.InstallationID == "" {
		return fmt.Errorf("%w: binding %q must name the principal it is granted to and the installation it is granted within",
			ErrInvalid, binding.ID)
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
