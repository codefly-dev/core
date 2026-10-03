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

	// The execution fields that used to live here — BuildIncarnation and
	// ImageDigest — MOVED to ApprovedBuild, and the move is the fix for a
	// blocker rather than a tidy-up.
	//
	// They were read from the OWNER's installation record, so a capability's
	// execution binding described the owner's workload however many
	// delegation hops had been added and whoever was actually exercising it.
	// A delegated hop's principal does not hold the owner's installation at
	// all, which is why there was nothing to attest a derivation against and
	// why execution-binding was closed only at Start.
	//
	// An execution belongs to a PRINCIPAL, not to a principal's use of an
	// installation, for the same reason an epoch does. ApprovedBuild answers
	// it per principal, and answers ErrNoApprovedBuild for a principal that
	// bears no execution at all.
}

// Execution is what a caller attests it is running, when it asks for a
// capability. It must come from the ORCHESTRATOR's record of that workload —
// the pod status as the API server reports it, never anything the process
// asserts over the wire — and the mint refuses unless it matches the approved
// build the issuer holds.
//
// # No consumer can source this correctly today, and the field still stays
//
// This said "the host resolves it after authenticating the workload", in the
// indicative, as though that were something a host does. The first implementer
// reported it is not: that host authenticates with a shared secret, performs
// no TokenReview, and has no Kubernetes API reader, so there is nothing it can
// consult about the pod at all. The requirement describes a capability that
// does not exist yet, and saying so is the difference between a known gap and
// a silent one.
//
// It stays required anyway, and that was the consumer's own call: dropping it
// would remove the only field that binds a credential to a build, and adding
// it back later is a breaking schema change made under pressure. What the
// consumer gets instead of a working check is an accurate description of what
// it owes — below — so the shortcut is a recorded debt rather than a
// misunderstanding.
//
// This is one of three instances of one shape, named and tabulated in
// docs/architecture.md under "A required input needs an independent source".
// A consumer integrating against all three saw the pattern that none of the
// three showed on its own.
//
// # The way to satisfy this field and defeat it at the same time
//
// A host that fills this from THE SAME RECORD IT SEALED has written a
// tautology. Seal.ImageDigest is the APPROVED build; Execution.ImageDigest is
// what is RUNNING. Filling both from the admitted presence document makes the
// mint compare approved against approved, so the check passes for every
// caller — including the superseded pod this field exists to refuse. The
// defect it closes is reintroduced by the field that closes it.
//
// Core cannot detect that. It cannot know where a caller got the bytes, both
// fields are strings, and the tautology type-checks. So this is the one
// requirement in this package that rests on the implementer rather than on a
// check, and the first implementer reported that the shortcut was the only
// option available to it — which is exactly how it gets taken.
//
// The test that catches it is not "does a sound execution mint". It is: take a
// pod from a SUPERSEDED generation, and confirm the mint refuses. If that
// passes while the approved build is the only digest the host can reach, the
// digest is being read from the wrong place.
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

	// Incarnation is the binding's current incarnation. Revision changes when
	// the binding's TERMS change; Incarnation changes when the binding is
	// withdrawn and re-created under the same ID, so a capability sealed to
	// the old one does not verify against the new.
	//
	// WHERE IT COMES FROM is a seam core does not close, and saying so is
	// better than leaving it implied. Nothing in this repository derives it:
	// an issuer holds it, and the only rule core states is the monotonicity
	// contract above — it only advances, and a reassignment advances the
	// revision. A host that re-creates a binding under a reused ID without
	// advancing the incarnation re-admits every capability sealed to the old
	// one, and core cannot detect that, exactly as it cannot detect a rewound
	// digest. The counterpart in solutionhost is the presence generation,
	// which IS derived from the delivered document; a binding has no such
	// document, so the issuer is the only source.
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

// ErrNoApprovedBuild is what a SealSource returns from ApprovedBuild when a
// principal BEARS NO EXECUTION. It is not a failure and not a missing record:
// it is the answer for a human session, which runs no approved build.
//
// It exists as a distinct sentinel because the alternative — an empty digest —
// cannot be told apart from an issuer that has lost the record, and those two
// must not read alike. One means "this capability carries no execution and
// that is correct"; the other means "refuse, because the issuer cannot say
// what is approved".
var ErrNoApprovedBuild = errors.New("work context: principal bears no execution")

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

	// ApprovedBuild answers the execution the issuer approves for one
	// principal: the image-manifest digest of the build, and the incarnation
	// of the run. It is keyed on the PRINCIPAL and on nothing else.
	//
	// It returns ErrNoApprovedBuild when the principal is KNOWN TO BEAR NO
	// EXECUTION, which is the answer for a human session and is not an error.
	//
	// An UNKNOWN principal must be a refusal, not ErrNoApprovedBuild. The two
	// must not read alike: "bears none" mints a capability carrying no
	// execution that every verifier accepts, so answering it for a principal
	// the issuer simply has no record of hands a service principal a
	// credential nothing can revoke by replacing its build. Nothing recorded
	// is the most permissive answer this method can give, so it has to be
	// asserted rather than defaulted.
	//
	// Keyed per principal because that is what a delegation needs. The
	// execution used to be read from the owner's installation seal, so a
	// derived capability's execution described the owner's workload no matter
	// who was exercising it — and a hop's principal does not hold the owner's
	// installation, so there was nothing a derivation could be attested
	// against. The mint is now execution-bound at every hop, which is the
	// blocker this method exists to close.
	//
	// It takes NO attributes of the workload, deliberately. Resolving by
	// (service account, image digest) is exactly what lets a pod from a
	// superseded generation in: it would answer "approved" for whatever that
	// pod presents. The issuer answers what IT approves, the caller attests
	// what it is running, and a mismatch is a refusal.
	//
	// # THE MONOTONICITY CONTRACT, which is an implementer's to keep
	//
	// Core compares these values for EQUALITY and cannot detect a source that
	// moves backwards, so the rule has to be stated rather than enforced here.
	// A conforming source guarantees that, for one principal, the pair
	// (digest, incarnation) only ADVANCES: the incarnation never decreases,
	// and a change of digest comes with an increase in the incarnation.
	//
	// The second clause is the one that is easy to miss, and MemorySealSource
	// missed it: approve B at incarnation 5, then approve A again at
	// incarnation 5, and every capability sealed to A at 5 — which the move to
	// B revoked — verifies again. A swap-back at a fixed counter is the rewind
	// the rule exists to prevent, reached through the field the rule did not
	// cover. A source that genuinely must rewind is reconstructing state
	// rather than recording it, and belongs behind a fresh instance.
	ApprovedBuild(ctx context.Context, principalID string) (digest string, incarnation uint64, err error)

	// OperationBinding resolves one binding by its opaque ID, exactly. It
	// returns ErrNoBinding when there is none.
	//
	// # THE MONOTONICITY CONTRACT, which is an implementer's to keep
	//
	// As with ApprovedBuild, core compares these for EQUALITY and cannot
	// detect a source that moves backwards. For one binding ID: Revision and
	// Incarnation never decrease, Revoked is terminal, and A REASSIGNMENT —
	// a change of PrincipalID or InstallationID — COMES WITH AN INCREASE IN
	// THE REVISION.
	//
	// That last clause is the one that is easy to miss, and MemorySealSource
	// missed it. The verifier refuses a capability whose exercising principal
	// is not the binding's, so moving a binding to another principal revokes
	// every capability the first holds against it; moving it back at the same
	// revision re-admits them all, with no counter having moved.
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
	if sealed.GetInstallationRevision() != live.InstallationRevision {
		return fmt.Errorf("%w: sealed to installation revision %d, the issuer holds %d",
			ErrRevoked, sealed.GetInstallationRevision(), live.InstallationRevision)
	}
	// The EXECUTION, against the principal that exercises this capability
	// rather than against the owner's installation record.
	//
	// It used to be read from that installation record, which meant a derived
	// capability's execution described the OWNER's workload however many hops
	// had been added. So a hop was never bound to what it was running, and a
	// caller holding a parent capability derived children regardless of its
	// own build. That is the blocker; this is the check that closes it.
	if err := checkExecutionsAgainst(ctx, seals, wc, sealed); err != nil {
		return err
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

// checkExecutionAgainst holds a seal's execution against what the issuer
// approves for the principal exercising it, IN BOTH DIRECTIONS.
//
// Both directions is the whole content. An execution-bearing principal whose
// capability carries none is refused, which is the obvious half. A principal
// that bears NO execution whose capability carries one is refused too — that
// is a process claiming to be a workload, and if only the first half were
// checked, a human session could be handed an execution nobody approved and
// nothing would object.
//
// The correspondence is what makes the field's optionality honest rather than
// a hedge: a capability carries an execution exactly when its exercising
// principal bears one.
// checkExecutionsAgainst holds EVERY link's execution against what the issuer
// approves for that link's principal: the owner's from the seal, each hop's
// from its own actor message, in one loop.
//
// One loop over all of them, exactly as checkActorEpochsAgainst does for
// epochs, and for the same reason — which a regression proved the hard way.
// The seal carried ONE execution slot and a derivation overwrote it with the
// last hop's, so this function checked only the exercising principal.
// Superseding the OWNER's build then refused the owner's own capability while
// every child of it verified, and those children kept minting grandchildren. A
// credential that records only the last link's run cannot be revoked by
// replacing any earlier one.
func checkExecutionsAgainst(ctx context.Context, seals SealSource, wc *basev0.WorkContextV1, sealed *basev0.WorkSealV1) error {
	if err := checkExecutionAgainst(ctx, seals, "the task owner", wc.GetOwnerPrincipalId(),
		sealed.GetImageDigest(), sealed.GetBuildIncarnation()); err != nil {
		return err
	}
	for index, hop := range wc.GetActorChain() {
		label := fmt.Sprintf("actor hop %d", index)
		if err := checkExecutionAgainst(ctx, seals, label, hop.GetPrincipalId(),
			hop.GetImageDigest(), hop.GetBuildIncarnation()); err != nil {
			return err
		}
	}
	return nil
}

func checkExecutionAgainst(ctx context.Context, seals SealSource, label, exercising, carriedDigest string, carriedIncarnation uint64) error {
	digest, incarnation, err := seals.ApprovedBuild(ctx, exercising)
	bearsNone := errors.Is(err, ErrNoApprovedBuild)
	if err != nil && !bearsNone {
		return fmt.Errorf("work context: approved build for principal %q: %w", exercising, err)
	}
	carried := carriedDigest != "" || carriedIncarnation != 0
	switch {
	case bearsNone && carried:
		return fmt.Errorf("%w: %s (%s) is sealed to build %s incarnation %d, and that principal bears no execution the issuer approves",
			ErrRevoked, label, exercising, carriedDigest, carriedIncarnation)
	case bearsNone:
		return nil
	case !carried:
		return fmt.Errorf("%w: %s (%s) carries no execution, and that principal exercises build %s incarnation %d",
			ErrRevoked, label, exercising, digest, incarnation)
	}
	if carriedDigest != digest {
		return fmt.Errorf("%w: %s (%s) is sealed to build %s, the issuer approves %s",
			ErrRevoked, label, exercising, carriedDigest, digest)
	}
	if carriedIncarnation != incarnation {
		return fmt.Errorf("%w: %s (%s) is sealed to incarnation %d, the issuer holds %d, so this execution has been replaced",
			ErrRevoked, label, exercising, carriedIncarnation, incarnation)
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
	// The execution the caller attests must be the one the issuer approves for
	// the principal that will EXERCISE this capability, and the incarnation it
	// believes it belongs to must be the live one. Both are checked rather
	// than recorded: a mint that took the caller's word would hand a
	// superseded pod a capability sealed to the current execution, and the
	// refusal would surface later as an authentication failure in a process
	// that cannot explain it.
	//
	// Keyed on `exercising`, not on the owner's installation. That is the
	// blocker's fix: a hop's principal does not hold the owner's
	// installation, so an execution read from the installation record
	// described the owner's workload whoever was actually running.
	approvedDigest, approvedIncarnation, err := a.Seals.ApprovedBuild(ctx, exercising)
	bearsNoExecution := errors.Is(err, ErrNoApprovedBuild)
	if err != nil && !bearsNoExecution {
		return nil, nil, fmt.Errorf("work context: approved build for principal %q: %w", exercising, err)
	}
	attestedAny := attested.ImageDigest != "" || attested.BuildIncarnation != 0
	// A principal that bears no execution — a human session — attests none,
	// and attesting one anyway is refused rather than ignored. A process
	// claiming to be a workload is the thing this field exists to catch, and
	// silently dropping the claim would mint a capability whose seal says
	// something the caller tried to assert.
	if bearsNoExecution && attestedAny {
		return nil, nil, fmt.Errorf("%w: principal %q bears no execution the issuer approves, so it cannot attest build %s incarnation %d",
			ErrInvalid, exercising, attested.ImageDigest, attested.BuildIncarnation)
	}
	if !bearsNoExecution && !attestedAny {
		return nil, nil, fmt.Errorf("%w: principal %q exercises an approved execution, so the caller must attest the image digest and incarnation it is running",
			ErrInvalid, exercising)
	}
	if !bearsNoExecution {
		if attested.ImageDigest == "" || attested.BuildIncarnation == 0 {
			return nil, nil, fmt.Errorf("%w: an attested execution is a digest AND an incarnation; one without the other names a run nothing can check",
				ErrInvalid)
		}
		if attested.ImageDigest != approvedDigest {
			return nil, nil, fmt.Errorf("%w: the caller attests build %s and the issuer approves %s for principal %q",
				ErrRevoked, attested.ImageDigest, approvedDigest, exercising)
		}
		if attested.BuildIncarnation != approvedIncarnation {
			return nil, nil, fmt.Errorf("%w: the caller attests incarnation %d and the issuer holds %d for principal %q, so this execution has been replaced",
				ErrRevoked, attested.BuildIncarnation, approvedIncarnation, exercising)
		}
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
	}
	// Set as a PAIR or not at all, which is what the schema's own message rule
	// requires: the two fields describe one execution and one of them alone
	// names a run nothing can check.
	if !bearsNoExecution {
		seal.ImageDigest = &approvedDigest
		seal.BuildIncarnation = &approvedIncarnation
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
	builds   map[string]approvedBuild
}

// approvedBuild is one principal's approved execution, as a source holds it.
type approvedBuild struct {
	digest      string
	incarnation uint64
}

// NewMemorySealSource returns an empty source.
func NewMemorySealSource() *MemorySealSource {
	return &MemorySealSource{
		seals:    map[string]Seal{},
		epochs:   map[string]uint64{},
		bindings: map[string]OperationBinding{},
		builds:   map[string]approvedBuild{},
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
	if seal.InstallationRevision == 0 {
		// Zero is not a revision, and a capability sealed to one would compare
		// equal to a source that simply had nothing recorded.
		return fmt.Errorf("%w: a seal's installation revision starts at 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Monotone, for the reason PutEpoch and PutBinding are: a sealed value is
	// compared for EQUALITY, so moving one back re-admits every capability
	// sealed to the earlier value.
	key := sealKey(principalID, seal.InstallationID)
	if held, exists := s.seals[key]; exists {
		for _, field := range []struct {
			label  string
			held   uint64
			wanted uint64
		}{
			{"installation revision", held.InstallationRevision, seal.InstallationRevision},
		} {
			if field.wanted < field.held {
				return fmt.Errorf("%w: principal %q installation %q is at %s %d and it only advances; lowering it to %d would re-admit every capability sealed to the earlier one",
					ErrInvalid, principalID, seal.InstallationID, field.label, field.held, field.wanted)
			}
		}
	}
	s.seals[key] = seal
	return nil
}

// PutApprovedBuild records the execution the issuer approves for one
// principal. A principal with no record BEARS NO EXECUTION and ApprovedBuild
// answers ErrNoApprovedBuild for it — which is the correct answer for a human
// session rather than a gap to be filled in.
//
// Monotone in the incarnation, AND a digest change requires the incarnation to
// advance. The second half was missing and the comment here argued it was
// unnecessary — "the DIGEST may change freely, because approving a different
// build is not a rewind". That is wrong, and the counterexample is two writes:
// approve B at incarnation 5, then approve A again at incarnation 5, and every
// capability sealed to A at 5 that the move to B had revoked is re-admitted.
// A swap-back at a fixed counter is exactly the rewind the monotonicity rule
// exists to prevent, reached through the field the rule did not cover.
//
// So the pair (digest, incarnation) advances together: the incarnation is what
// separates two runs, and a run on a different build is a different run.
func (s *MemorySealSource) PutApprovedBuild(principalID, digest string, incarnation uint64) error {
	if principalID == "" {
		return fmt.Errorf("%w: an approved build is held for one principal", ErrInvalid)
	}
	if digest == "" || incarnation == 0 {
		// Both or neither, the same pairing the schema requires of a seal: one
		// without the other names a run nothing can check. A principal that
		// bears no execution is recorded by NOT calling this.
		return fmt.Errorf("%w: an approved build is a digest AND an incarnation starting at 1; to say a principal bears no execution, record none",
			ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, exists := s.builds[principalID]; exists {
		if incarnation < held.incarnation {
			return fmt.Errorf("%w: principal %q is at incarnation %d and it only advances; lowering it to %d would re-admit every capability sealed to the earlier run",
				ErrInvalid, principalID, held.incarnation, incarnation)
		}
		if held.digest != digest && incarnation == held.incarnation {
			return fmt.Errorf("%w: principal %q is approved for a different build at incarnation %d; a new build is a new run, so advance the incarnation — reusing it lets a swap back to the earlier digest re-admit every capability sealed to it",
				ErrInvalid, principalID, incarnation)
		}
	}
	s.builds[principalID] = approvedBuild{digest: digest, incarnation: incarnation}
	return nil
}

// PutBearsNoExecution records, EXPLICITLY, that a principal bears no execution
// — a human session, not a workload.
//
// It exists because an unknown principal used to answer ErrNoApprovedBuild,
// which made "nothing recorded" the most permissive answer this source gives:
// a service principal whose build record was simply missing was handed a
// capability carrying no execution, and the verifier accepted it. That is the
// fourth time this shape has appeared here — an empty signer policy meaning "a
// renderer", an empty digest meaning "bears no execution", a zero applied
// record meaning "first generation", and this.
//
// So an unknown principal is now a REFUSAL, and bearing no execution is a
// recorded fact like any other.
func (s *MemorySealSource) PutBearsNoExecution(principalID string) error {
	if principalID == "" {
		return fmt.Errorf("%w: a principal is named", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, exists := s.builds[principalID]; exists && held.digest != "" {
		return fmt.Errorf("%w: principal %q is approved for build %s at incarnation %d; a workload does not become a human, and declaring it bears no execution would re-admit every capability that carries none",
			ErrInvalid, principalID, held.digest, held.incarnation)
	}
	s.builds[principalID] = approvedBuild{}
	return nil
}

// ApprovedBuild answers the execution approved for one principal,
// ErrNoApprovedBuild when it is recorded as bearing none, and a REFUSAL when
// the principal is unknown.
//
// An unknown principal is not "bears no execution". It is an issuer that
// cannot say, and the two must not read alike: the first would mint a
// capability carrying no execution that every verifier accepts, which is how a
// service principal with a missing record gets a credential nobody can revoke
// by replacing its build.
func (s *MemorySealSource) ApprovedBuild(_ context.Context, principalID string) (string, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	held, exists := s.builds[principalID]
	if !exists {
		return "", 0, fmt.Errorf("work context: no execution record for principal %q; record an approved build, or PutBearsNoExecution to say it bears none",
			principalID)
	}
	if held.digest == "" {
		return "", 0, fmt.Errorf("%w: principal %q", ErrNoApprovedBuild, principalID)
	}
	return held.digest, held.incarnation, nil
}

// PutBinding records the live state of one operation binding.
//
// Revision and incarnation only ADVANCE, and Revoked is TERMINAL, for the same
// reason PutEpoch refuses to lower an epoch: both are compared for equality
// against a capability's sealed values, so moving one BACK re-admits every
// capability sealed to the earlier value, and clearing Revoked resurrects
// every capability the withdrawal refused. A source that genuinely must rewind
// is reconstructing state rather than recording it, and should be built
// afresh.
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
	if held, exists := s.bindings[binding.ID]; exists {
		if held.Revoked && !binding.Revoked {
			return fmt.Errorf("%w: binding %q is revoked and a withdrawal is terminal; clearing it would resurrect every capability sealed to it",
				ErrInvalid, binding.ID)
		}
		if binding.Revision < held.Revision {
			return fmt.Errorf("%w: binding %q is at revision %d and a revision only advances; lowering it to %d would re-admit every capability sealed to the earlier one",
				ErrInvalid, binding.ID, held.Revision, binding.Revision)
		}
		if binding.Incarnation < held.Incarnation {
			return fmt.Errorf("%w: binding %q is at incarnation %d and an incarnation only advances; lowering it to %d would re-admit a capability from a replaced binding",
				ErrInvalid, binding.ID, held.Incarnation, binding.Incarnation)
		}
		// REASSIGNMENT ADVANCES THE REVISION, and this is the swap-back the
		// counter rules above do not cover — C8's shape, one field over.
		//
		// The verifier refuses a capability whose exercising principal is not
		// the binding's, so moving a binding to another principal REVOKES
		// every capability the first principal holds against it. Moving it
		// back at the same revision re-admits them all. The counters never
		// moved, so neither monotonicity rule fired, and the identity fields
		// had no rule at all.
		//
		// Advancing the revision is what makes the revocation stick: the
		// verifier compares it for equality, so the capabilities refused by
		// the move stay refused after the move back. A reassignment is a
		// change to the binding's terms, which is what a revision is for.
		for _, identity := range []struct {
			what  string
			held  string
			moved string
		}{
			{"principal", held.PrincipalID, binding.PrincipalID},
			{"installation", held.InstallationID, binding.InstallationID},
		} {
			if identity.held != identity.moved && binding.Revision == held.Revision {
				return fmt.Errorf("%w: binding %q is granted to %s %q and this moves it to %q at the same revision %d; advance the revision, because moving a binding away revokes every capability held against it and moving it back at a fixed revision would re-admit them",
					ErrInvalid, binding.ID, identity.what, identity.held, identity.moved, held.Revision)
			}
		}
	}
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
	// THIS authority's own capability, or nothing. A *Verified only proves
	// that SOME verifier accepted the token, and a verifier is pinned to one
	// issuer — so a capability another issuer minted and its own verifier
	// accepted was a usable parent here. Measured before this check existed:
	// our authority derived a child from a foreign issuer's parent, and the
	// child was sealed, signed and verifiable as ours.
	//
	// Everything else in a derivation is held against OUR state — our
	// revision, our seals, our bindings — so without this the only thing the
	// parent contributed was authority nobody here granted.
	if issued := parent.claims().GetIssuer(); issued != a.Issuer {
		return 0, fmt.Errorf("%w: the parent was issued by %q and this authority is %q; a capability derives only from its own issuer",
			ErrInvalid, issued, a.Issuer)
	}
	inherited := parent.claims().GetAuthorizationRevision()
	current, err := a.revision(ctx, parent.claims().GetTenantId())
	if err != nil {
		return 0, err
	}
	if inherited != current {
		return 0, fmt.Errorf("%w: the parent was minted at authorization revision %d and the issuer is at %d, so it can derive nothing; mint afresh",
			ErrRevoked, inherited, current)
	}
	return current, nil
}
