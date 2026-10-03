package workcontext

import (
	"context"
	"errors"
	"fmt"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Seal is the live binding of a principal's authority to one installation and
// one execution, as the issuer holds it right now. A Verifier compares the
// capability's sealed values against this field by field, for exact equality.
//
// Exact equality, not "at least": a capability carrying a revision HIGHER than
// the issuer's live one is refused too. There is no legitimate way to hold one
// — it would mean a capability sealed to an installation state that has not
// happened — so the shapes that could produce it are a rolled-back
// installation and a forged seal, and neither is a thing to accept.
// There is deliberately no PrincipalEpoch field. An epoch belongs to a
// PRINCIPAL, not to a principal's use of one installation, and carrying it
// here made it answerable from two places at once. That cost two real
// defects, both measured before this was collapsed:
//
//   - PutEpoch on the owner raised epochs[owner] and left every stored seal
//     untouched, so revoking the owner did not refuse the owner's own
//     sessions — the verifier read the stale copy out of the seal.
//   - Put for a second installation overwrote epochs[principal] with that
//     seal's epoch, lowering it. A principal revoked by PutEpoch was
//     UN-revoked by recording an unrelated installation.
//
// The epoch is now answered only by SealSource.PrincipalEpoch, for the owner
// and for every actor hop alike, through one code path.
type Seal struct {
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

	// ImageDigest is the APPROVED BUILD for this installation, as an OCI
	// image-manifest digest. It is the issuer's answer about what is approved,
	// never a workload's claim about itself.
	ImageDigest string
}

// Execution is what a caller attests it is running, when it asks for a
// capability. The host resolves it after authenticating the workload — from
// the pod's own status, not from anything the process asserts over the wire —
// and the mint refuses unless it matches the approved build the issuer holds.
//
// It is an INPUT because only the host can know it, and it is CHECKED because
// an input nobody checks is a claim. Before this existed, the minter read the
// current incarnation for a principal and stamped it, so a pod from a
// superseded generation could mint itself a capability sealed to the current
// execution — which is the exact threat the build_incarnation field was
// introduced to prevent, left open by the mint.
type Execution struct {
	// ImageDigest is the image-manifest digest the caller is running.
	ImageDigest string

	// BuildIncarnation is the incarnation the caller believes it belongs to.
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
	//
	// It does NOT answer the principal's epoch: that is PrincipalEpoch's, for
	// every principal including the owner, so there is exactly one source for
	// it. See the Seal type.
	Seal(ctx context.Context, principalID, installationID string) (Seal, error)

	// PrincipalEpoch answers the live epoch of one principal, independent of
	// any installation. It is THE source for every epoch comparison — the
	// task owner's and every actor hop's.
	//
	// It is independent of Seal because an epoch belongs to a principal
	// rather than to a principal's use of one installation, and because an
	// ACTOR's authority is narrowed independently of the owner's: a delegated
	// operation context may have a person as its owner and a service
	// principal as its actor, and narrowing that service principal has to
	// reach every capability it acts in. It returns ErrNoSeal when the
	// principal is not known.
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

// sealOf reads the capability's seal.
//
// It no longer has an error to return. The seal is REQUIRED by the schema and
// its installation id has a minimum length, so protovalidate refuses a
// capability carrying neither — and protovalidate runs in decodeClaims, before
// anything here is reached. The sentinel this used to return, ErrUnsealed, is
// deleted with it: a sentinel no branch can produce is worse than no sentinel,
// because a consumer writes a handler for it and the handler never runs.
func sealOf(wc *basev0.WorkContextV1) *basev0.WorkSealV1 { return wc.GetSeal() }

// checkSeal holds a capability's seal against the issuer's live values, and its
// operation binding against the one binding its sealed ID resolves to.
//
// Every mismatch is ErrRevoked rather than ErrInvalid. The capability is not
// malformed and was not forged: it was sound when it was minted and the state
// it was sealed to has moved, which is the same situation a superseded
// authorization revision describes, and a caller distinguishing "re-mint" from
// "reject this caller" needs them to read alike.
func checkSealAgainst(ctx context.Context, seals SealSource, wc *basev0.WorkContextV1) error {
	sealed := sealOf(wc)
	// The principal the seal is held for is the task's OWNER: the installation
	// is the owner's, and a delegation hop narrows authority within it rather
	// than moving it. Each actor's own epoch is checked separately below,
	// because an actor is narrowed independently of the owner.
	owner := wc.GetOwnerPrincipalId()
	live, err := seals.Seal(ctx, owner, sealed.GetInstallationId())
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
		{"installation revision", sealed.GetInstallationRevision(), live.InstallationRevision},
		{"build incarnation", sealed.GetBuildIncarnation(), live.BuildIncarnation},
	} {
		if field.sealed != field.live {
			return fmt.Errorf("%w: sealed to %s %d, the issuer holds %d", ErrRevoked, field.label, field.sealed, field.live)
		}
	}
	// The approved build, compared exactly. A capability sealed to a build the
	// issuer no longer approves is not a credential, however current its
	// counters are.
	if sealed.GetImageDigest() != live.ImageDigest {
		return fmt.Errorf("%w: sealed to build %s, the issuer approves %s", ErrRevoked, sealed.GetImageDigest(), live.ImageDigest)
	}
	// The OWNER's epoch, from the one source, exactly as every hop's is read.
	if err := checkEpochAgainst(ctx, seals, "the task owner", owner, sealed.GetPrincipalEpoch()); err != nil {
		return err
	}
	if err := checkActorEpochsAgainst(ctx, seals, wc); err != nil {
		return err
	}
	return checkOperationBindingAgainst(ctx, seals, wc, sealed)
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
func checkActorEpochsAgainst(ctx context.Context, seals SealSource, wc *basev0.WorkContextV1) error {
	for index, hop := range wc.GetActorChain() {
		// A hop carrying no epoch is not revocable. The SCHEMA refuses one
		// now, with required plus gte=1, so there is no check for it here —
		// a shape that cannot reach this function needs no branch.
		label := fmt.Sprintf("actor hop %d", index)
		if err := checkEpochAgainst(ctx, seals, label, hop.GetPrincipalId(), hop.GetPrincipalEpoch()); err != nil {
			return err
		}
	}
	return nil
}

// checkEpoch holds one sealed epoch against that principal's live epoch, from
// the single source. The owner and every hop go through it, so the owner
// cannot be compared against a different answer than an actor is.
func checkEpochAgainst(ctx context.Context, seals SealSource, label, principalID string, sealed uint64) error {
	current, err := seals.PrincipalEpoch(ctx, principalID)
	if err != nil {
		if errors.Is(err, ErrNoSeal) {
			return fmt.Errorf("%w: %s names principal %q, which the issuer does not hold", ErrRevoked, label, principalID)
		}
		return fmt.Errorf("work context: epoch for principal %q: %w", principalID, err)
	}
	if sealed != current {
		return fmt.Errorf("%w: %s (%s) is sealed to epoch %d, the issuer holds %d",
			ErrRevoked, label, principalID, sealed, current)
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
func checkOperationBindingAgainst(ctx context.Context, seals SealSource, wc *basev0.WorkContextV1, sealed *basev0.WorkSealV1) error {
	binding := wc.GetOperationBinding()
	if binding == nil {
		return nil
	}
	// Exact lookup by the sealed ID. Nothing here chooses a binding; the ID
	// chose it when the capability was minted, and this either finds that one
	// binding or refuses.
	resolved, err := seals.OperationBinding(ctx, binding.GetBindingId())
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
func (a *Authority) sealFor(ctx context.Context, principalID, installationID, bindingID, exercising string, attested Execution) (*basev0.WorkSealV1, *basev0.WorkOperationBindingV1, error) {
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
	// The execution the caller attests must be the approved one, and the
	// incarnation it believes it belongs to must be the live one. BOTH are
	// checked rather than recorded: a mint that took the caller's word would
	// hand a superseded pod a capability sealed to the current execution, and
	// the refusal would surface later as an authentication failure in a
	// process that cannot explain it.
	if attested.ImageDigest == "" || attested.BuildIncarnation == 0 {
		return nil, nil, fmt.Errorf("%w: a capability is sealed to one execution, so the caller must attest the image digest and incarnation it is running",
			ErrInvalid)
	}
	if live.ImageDigest == "" {
		return nil, nil, fmt.Errorf("work context: the seal source holds no approved build for principal %q installation %q, so no execution can be matched against it",
			principalID, installationID)
	}
	if attested.ImageDigest != live.ImageDigest {
		return nil, nil, fmt.Errorf("%w: the caller attests build %s and the approved build for installation %q is %s",
			ErrRevoked, attested.ImageDigest, installationID, live.ImageDigest)
	}
	if attested.BuildIncarnation != live.BuildIncarnation {
		return nil, nil, fmt.Errorf("%w: the caller attests incarnation %d and the issuer holds %d, so this execution has been replaced",
			ErrRevoked, attested.BuildIncarnation, live.BuildIncarnation)
	}
	// The epoch comes from the one source, never from the seal record.
	epoch, err := a.epochFor(ctx, principalID)
	if err != nil {
		return nil, nil, err
	}
	seal := &basev0.WorkSealV1{
		PrincipalEpoch:       epoch,
		InstallationId:       live.InstallationID,
		InstallationRevision: live.InstallationRevision,
		BuildIncarnation:     live.BuildIncarnation,
		ImageDigest:          live.ImageDigest,
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
func (a *Authority) carryForwardSeal(ctx context.Context, parent *basev0.WorkContextV1) error {
	if a.Seals == nil {
		return fmt.Errorf("work context: authority has no seal source")
	}
	// THE SAME CHECK THE VERIFIER MAKES, not a subset of it. This used to be a
	// second implementation that compared the inherited installation, epoch and
	// build, and the carried binding's revision and incarnation — and left out
	// the binding's ENTITLEMENT and the inherited hops' epochs. A reviewer
	// reproduced the consequence by execution: reassign the parent's binding
	// to another principal and Verify(parent) refuses it with ErrRevoked,
	// while Child(parent, replacementBinding) minted a credential that
	// verified. The parent regained authority by replacing the binding that
	// had been taken from it.
	//
	// The lesson is the one this whole PR is about, one level down: two
	// implementations of a check, kept in step by hand, and the shorter one
	// decides. So there is one now. A derivation asks exactly "would this
	// parent verify right now", and anything that would refuse the parent
	// refuses the derivation.
	// Checked on the PARENT's claims, not on the half-built child: the child
	// has its new hop appended but not yet stamped with an epoch, so running
	// this over it would refuse every derivation for the child's own missing
	// epoch and hide whatever is actually wrong with the parent.
	if err := checkSealAgainst(ctx, a.Seals, parent); err != nil {
		return fmt.Errorf("%w; the parent cannot derive, so mint afresh", err)
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

// PutEpoch records one principal's live epoch. It is the ONLY writer of an
// epoch in this source, for owners and actors alike — see Put.
//
// It refuses to LOWER an epoch. An epoch only ever advances, and advancing it
// is a revocation, so accepting a lower value would be un-revoking a
// principal — which is how the defect this replaced actually bit. A source
// that genuinely must rewind one is reconstructing state rather than
// recording it, and should be built afresh.
func (s *MemorySealSource) PutEpoch(principalID string, epoch uint64) error {
	if principalID == "" || epoch == 0 {
		return fmt.Errorf("%w: a principal's epoch starts at 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, exists := s.epochs[principalID]; exists && epoch < held {
		return fmt.Errorf("%w: principal %q is at epoch %d and an epoch only advances; lowering it to %d would un-revoke every capability it acts in",
			ErrInvalid, principalID, held, epoch)
	}
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
// it was asked about.
//
// It does NOT touch the principal's epoch. An earlier version did, "so an
// owner is answerable as an actor without a second call", and that convenience
// was a revocation bug: recording a seal for a second installation overwrote
// the epoch with that seal's value, so a principal revoked by PutEpoch was
// UN-revoked by recording an unrelated installation. The epoch has one writer
// now, PutEpoch, and one reader, PrincipalEpoch.
func (s *MemorySealSource) Put(principalID string, seal Seal) error {
	if principalID == "" || seal.InstallationID == "" {
		return fmt.Errorf("%w: a seal is held for one principal's use of one installation", ErrInvalid)
	}
	if seal.ImageDigest == "" {
		return fmt.Errorf("%w: a seal names the approved build for the installation; without one no execution can be matched against it", ErrInvalid)
	}
	if seal.InstallationRevision == 0 || seal.BuildIncarnation == 0 {
		// Zero is not a revision, and a capability sealed to one would compare
		// equal to a source that simply had nothing recorded.
		return fmt.Errorf("%w: a seal's revision and incarnation both start at 1", ErrInvalid)
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

// carryForwardRevision holds the PARENT's authorization revision against the
// issuer's current one and refuses the derivation when the parent is
// superseded. It returns the current revision for the child to carry.
//
// This is the tenant-wide lever, and it was laundered the same way the seal
// was. The original comment here read "the issuer's revision now, not the
// parent's: a bump between the parent's verification and this mint would
// otherwise produce a child that every verifier rejects as superseded" — which
// is true, and is the wrong conclusion. A parent that every verifier rejects
// as superseded is a REVOKED credential, and the answer to "deriving from it
// would produce something dead" is to refuse the derivation, not to stamp the
// child with a number that makes it live again:
//
//  1. verify a session at revision 7;
//  2. the issuer bumps to 8, and Verify itself calls that ErrRevoked;
//  3. call Child with the held *Verified — and before this fix the child
//     carried revision 8 and verified.
//
// So the tenant-wide revocation lever was escapable by deriving once, exactly
// as the seal's was.
//
// The PR that fixed the seal argued an asymmetry: the revision is monotonic
// and wants re-reading, the seal is compared for equality and wants holding.
// That argument is wrong and a second reviewer was right to reject it. Both
// are revocation levers. Monotonicity says only that the comparison is < rather
// than !=; it says nothing about whether a derivation may cross a bump, and
// re-reading EITHER lever at the mint launders it. The seal looked different
// only because its mismatch was already being surfaced as ErrRevoked.
//
// What re-reading legitimately buys is still kept: once the parent is known
// current, the child carries the current revision rather than the parent's, so
// a concurrent bump between this check and the next verification is the
// ordinary race it always was and not a stale stamp.
func (a *Authority) carryForwardRevision(ctx context.Context, parent *Verified) (uint64, error) {
	inherited := parent.Context().GetAuthorizationRevision()
	current, err := a.revision(ctx, parent.Context().GetTenantId())
	if err != nil {
		return 0, err
	}
	if inherited != current {
		return 0, fmt.Errorf("%w: the parent was minted at authorization revision %d and the issuer is at %d, so it can derive nothing; mint afresh",
			ErrRevoked, inherited, current)
	}
	return current, nil
}
