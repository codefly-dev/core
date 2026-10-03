package solutionhost

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaAuthorityV1 is the only authority schema this package accepts. Like the
// presence schema it is part of the signed canonical encoding, which is what
// binds a signature to the document TYPE: an authority document cannot be
// presented as a presence document under a signature that verifies.
const SchemaAuthorityV1 = "codefly/solution-authority/v1"

// AuthorityFileName is the conventional name of a rendered authority document.
const AuthorityFileName = "solution-authority.codefly.yaml"

var (
	// ErrOutsideEnvelope means an authority document claims more than the
	// ceiling the caller supplied allows: a binding the envelope does not hold,
	// or a build the envelope has not approved.
	ErrOutsideEnvelope = errors.New("solution authority claims more than its envelope allows")

	// ErrNotActivated means an authority document and a presence document do
	// not form a matched tuple, so neither grants anything. It is the error for
	// every way the two halves can fail to line up — a different host, a
	// different ownership domain, a different envelope revision, a build one
	// names and the other does not, or an authority effective from a generation
	// the presence document has not reached.
	//
	// One sentinel, because the response is the same for all of them: nothing
	// is active, and the half that is wrong is named in the message.
	ErrNotActivated = errors.New("solution authority and presence do not activate")
)

// Authority IDs, principal IDs and binding IDs come from the host and are
// opaque here: core compares them, and never derives, parses or subsets them.
var opaqueIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}[A-Za-z0-9]$`)

// AuthorityDocument is the reviewed, signed record of what authority principals
// hold on one host — the static half of authorization, as the presence document
// is the static half of presence.
//
// It grants nothing on its own. Authority is active only as part of a matched
// (authority, presence, build) tuple: see Activate. That is what makes
// "approved for one exact build" a property of the system rather than of a
// field — an authority document for a build the host is not running, or a
// presence document with no authority behind it, both activate nothing.
//
// It is also not the dynamic half. The envelope — the ceiling a platform
// administrator writes — an organisation's installation of a module, its scope
// within the ceiling, and team exposure are the host's, and change at runtime.
// This document carries only the identities and revisions of those, so that it
// can be held against them and so a credential can seal them.
type AuthorityDocument struct {
	// Schema is the document's version, checked first and covered by the
	// signature.
	Schema string `yaml:"schema" json:"schema"`

	// Authority is the stable ID of this authority document.
	Authority string `yaml:"authority" json:"authority"`

	// Generation is strictly monotonic per authority ID and starts at 1.
	//
	// Detecting a replay takes more than monotonicity, and an earlier version
	// of this comment claimed the detection existed when nothing performed it:
	// Activate never compared this field against anything, and keyless
	// signatures do not expire, so a genuinely signed document at generation
	// N-1 re-granted every binding generation N had withdrawn. The fold is
	// AppliedAuthority and decideAuthority, and Activate requires the applied
	// record — see ActivationRequest.
	Generation uint64 `yaml:"generation" json:"generation"`

	// PresenceBinding is the presence binding ID this authority is granted
	// over, and it must be that document's own. The YAML key is `binding`; the
	// Go field is not, because AuthorityDocument already has a Binding method
	// that resolves one of its own AuthorityBindings by id.
	//
	// Without it, Activate matched on host, domain, envelope revision and
	// build membership only — so an authority document activated ANY binding
	// in the same host and domain running the same image, including a new
	// instance that had taken a tombstoned binding's alias. Activation.Binding
	// was simply copied from whichever presence document the caller passed in,
	// which made the field look like a target while being an echo.
	PresenceBinding string `yaml:"binding" json:"binding"`

	// Host is the coordinate and component this document grants on. A document
	// delivered to another host grants nothing there.
	Host HostTarget `yaml:"host" json:"host"`

	// OwnershipDomain is the slice of the host's binding space this document
	// speaks for, and it must be the presence document's own: authority
	// delivered for one domain never activates presence in another.
	OwnershipDomain string `yaml:"ownership_domain" json:"ownership_domain"`

	// EnvelopeRevision is the revision of the ceiling this document was
	// validated against. Required and at least 1. Both halves of a tuple carry
	// it and both must agree, so authority validated against a wider ceiling
	// cannot activate presence validated against a narrower one.
	EnvelopeRevision uint64 `yaml:"envelope_revision" json:"envelope_revision"`

	// ApprovedBuild is the OCI image manifest digest this authority is
	// effective for — one exact build, reviewed and approved. Authority is
	// never held for "the solution"; it is held for the build that was
	// approved, so shipping a new build does not silently carry the old
	// build's authority forward.
	ApprovedBuild ImageDigest `yaml:"approved_build" json:"approved_build"`

	// EffectiveFrom is the presence generation this authority is effective
	// from. A presence document at an earlier generation does not activate it,
	// which is what keeps authority from preceding the delivery it describes.
	EffectiveFrom uint64 `yaml:"effective_from" json:"effective_from"`

	// Principals are the principals this document grants to, each with the
	// bindings it holds. At least one unless this generation is a tombstone.
	Principals []PrincipalAuthority `yaml:"principals,omitempty" json:"principals,omitempty"`

	// Removed marks this generation a tombstone: the authority is declared
	// withdrawn. As with presence, removal is a generation and never the
	// absence of a document, so an unreadable mount can never be read as
	// "withdraw every authority".
	Removed bool `yaml:"removed,omitempty" json:"removed,omitempty"`
}

// PrincipalAuthority is one principal and the bindings it holds.
type PrincipalAuthority struct {
	// Principal is the principal's ID, opaque here.
	Principal string `yaml:"principal" json:"principal"`

	// Bindings are the units of authority this principal holds. At least one:
	// a principal listed with no binding is a row that grants nothing and
	// reads as if it grants something.
	Bindings []AuthorityBinding `yaml:"bindings" json:"bindings"`
}

// AuthorityBinding is one unit of authority: an operation audience, a scope, a
// queue and a namespace, under an ID the host minted.
//
// The ID is the identity and it is opaque TO CORE: core compares IDs and never
// derives, parses or subsets one, so an exact lookup can never quietly become a
// search. That is the property the credential contract rests on — nothing has
// to decide whether one binding "contains" another.
//
// Whether delivery derives the ID deterministically from the contract it
// renders is delivery's business, and deterministic is better than random
// because it is stable across renders. Predictability costs nothing here: an ID
// is neither a secret nor a capability, authority comes from the signed
// document that lists it, and an ID a caller invents simply is not in the
// document or the host's store. See docs/solution-host-binding.md.
type AuthorityBinding struct {
	// ID is the host's opaque identifier for this binding. It is what a
	// credential seals and what a verifier looks up.
	ID string `yaml:"id" json:"id"`

	// Revision is this binding's revision, monotonic per ID and at least 1. A
	// credential seals it, and a verifier holding a different one refuses.
	Revision uint64 `yaml:"revision" json:"revision"`

	// Audience is the operation audience the binding grants against.
	Audience string `yaml:"audience" json:"audience"`

	// Scope is what it grants within that audience.
	Scope string `yaml:"scope" json:"scope"`

	// Queue is the queue the operation runs on. OPTIONAL.
	//
	// Absence means this binding grants NO queue-scoped authority. It never
	// means every queue. A module that owns no queue is a real case, and the
	// alternative — requiring the field — leaves every such module with no
	// derivable authority document at all.
	//
	// What makes absence safe is that envelope containment is exact element
	// inclusion: an absent queue in a document matches only an absent queue in
	// the envelope. So a document cannot claim a queue it was not granted, and
	// absence cannot widen into a wildcard — it can only match absence. A host
	// reading absence as "any queue" is the failure this field would otherwise
	// invite, and ValidateAgainst is what forecloses it.
	Queue string `yaml:"queue,omitempty" json:"queue,omitempty"`

	// Namespace is the namespace it runs in. OPTIONAL, on the same terms as
	// Queue: absence grants no namespace-scoped authority and never every
	// namespace.
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// Envelope is the ceiling an authority document is validated against: the
// bindings a platform administrator wrote, the builds that have been approved,
// and the revision both halves of a tuple must agree on.
//
// It is supplied by the caller and never read out of a document. An envelope a
// document carried would be a document declaring its own ceiling.
type Envelope struct {
	// Revision is this envelope's revision. A document naming a different one
	// was validated against a different ceiling.
	Revision uint64

	// Bindings are the units of authority the ceiling allows, in full. A
	// document's binding is inside the ceiling when this list holds it
	// exactly — see ValidateAgainst.
	Bindings []AuthorityBinding

	// ApprovedBuilds are the builds this envelope has approved.
	ApprovedBuilds []ImageDigest
}

// ParseAuthority decodes and validates one authority document. Decoding is
// strict, for the same reason Parse is: an unknown field is an error, so a
// document cannot smuggle a key, a certificate or an envelope of its own past a
// verifier that would otherwise ignore it.
func ParseAuthority(data []byte) (*AuthorityDocument, error) {
	document, err := decodeStrict[AuthorityDocument](data, "solution authority", SchemaAuthorityV1)
	if err != nil {
		return nil, err
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return document, nil
}

// MarshalAuthority renders a validated authority document.
func MarshalAuthority(document *AuthorityDocument) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(document)
}

// Validate checks everything one authority document can be checked against on
// its own. Whether it is inside a ceiling is ValidateAgainst; whether it is
// active is Activate. Neither is a property of the document alone, and both
// need something the document must never carry.
func (document *AuthorityDocument) Validate() error {
	if document == nil {
		return fmt.Errorf("%w: document is required", ErrInvalid)
	}
	if document.Schema != SchemaAuthorityV1 {
		return fmt.Errorf("%w: %q (this Core reads %q)", ErrSchema, document.Schema, SchemaAuthorityV1)
	}
	if !opaqueIDPattern.MatchString(document.Authority) {
		return fmt.Errorf("%w: authority ID %q is invalid", ErrInvalid, document.Authority)
	}
	if document.Generation == 0 {
		return fmt.Errorf("%w: generation starts at 1", ErrInvalid)
	}
	if !bindingPattern.MatchString(document.PresenceBinding) || document.PresenceBinding == reservedOwner {
		return fmt.Errorf("%w: authority %q names binding %q, which is not a valid binding ID; authority is granted over one presence binding and never over a host and domain at large",
			ErrInvalid, document.Authority, document.PresenceBinding)
	}
	if !namePattern.MatchString(document.Host.Coordinate) {
		return fmt.Errorf("%w: host coordinate %q is invalid", ErrInvalid, document.Host.Coordinate)
	}
	if !namePattern.MatchString(document.Host.Component) {
		return fmt.Errorf("%w: host component %q is invalid", ErrInvalid, document.Host.Component)
	}
	if !namePattern.MatchString(document.OwnershipDomain) {
		return fmt.Errorf("%w: ownership domain %q is invalid", ErrInvalid, document.OwnershipDomain)
	}
	if document.EnvelopeRevision == 0 {
		return fmt.Errorf("%w: envelope revision starts at 1; 0 names no ceiling", ErrInvalid)
	}
	if document.Removed {
		for _, declared := range []struct {
			label string
			count int
		}{{"principals", len(document.Principals)}} {
			if declared.count != 0 {
				return fmt.Errorf("%w: a removed generation declares no %s", ErrInvalid, declared.label)
			}
		}
		// A tombstone withdraws authority, so it is effective for no build and
		// from no generation. Carrying either would be a withdrawal that still
		// names what it grants.
		if document.ApprovedBuild != "" {
			return fmt.Errorf("%w: a removed generation approves no build", ErrInvalid)
		}
		if document.EffectiveFrom != 0 {
			return fmt.Errorf("%w: a removed generation is effective from no generation", ErrInvalid)
		}
		return nil
	}
	if !digestPattern.MatchString(string(document.ApprovedBuild)) {
		return fmt.Errorf("%w: authority %q requires the SHA-256 OCI image manifest digest of the build it is approved for, got %q",
			ErrInvalid, document.Authority, document.ApprovedBuild)
	}
	if document.EffectiveFrom == 0 {
		return fmt.Errorf("%w: authority %q must name the presence generation it is effective from", ErrInvalid, document.Authority)
	}
	if len(document.Principals) == 0 {
		return fmt.Errorf("%w: a present generation grants to at least one principal", ErrInvalid)
	}
	return document.validatePrincipals()
}

func (document *AuthorityDocument) validatePrincipals() error {
	seenPrincipal := make(map[string]struct{}, len(document.Principals))
	// Binding IDs are unique across the whole document, not per principal: the
	// credential contract is an exact lookup by ID, so one ID naming two
	// different units of authority would make that lookup ambiguous in the one
	// place that must never guess.
	seenBinding := make(map[string]string)
	for _, principal := range document.Principals {
		if !opaqueIDPattern.MatchString(principal.Principal) {
			return fmt.Errorf("%w: principal ID %q is invalid", ErrInvalid, principal.Principal)
		}
		if _, exists := seenPrincipal[principal.Principal]; exists {
			return fmt.Errorf("%w: principal %q is declared twice", ErrInvalid, principal.Principal)
		}
		seenPrincipal[principal.Principal] = struct{}{}
		if len(principal.Bindings) == 0 {
			return fmt.Errorf("%w: principal %q holds no binding; a principal that grants nothing is not declared", ErrInvalid, principal.Principal)
		}
		for _, binding := range principal.Bindings {
			if err := binding.validate(principal.Principal); err != nil {
				return err
			}
			if owner, exists := seenBinding[binding.ID]; exists {
				return fmt.Errorf("%w: binding ID %q is held by both %q and %q; an ID identifies one unit of authority",
					ErrInvalid, binding.ID, owner, principal.Principal)
			}
			seenBinding[binding.ID] = principal.Principal
		}
	}
	return nil
}

func (binding AuthorityBinding) validate(principal string) error {
	if !opaqueIDPattern.MatchString(binding.ID) {
		return fmt.Errorf("%w: principal %q binding ID %q is invalid", ErrInvalid, principal, binding.ID)
	}
	if binding.Revision == 0 {
		return fmt.Errorf("%w: principal %q binding %q revision starts at 1", ErrInvalid, principal, binding.ID)
	}
	// The audience and the scope are what the binding grants against and what
	// it grants; neither can be absent without the binding granting something
	// unstated.
	for _, part := range []struct{ label, value string }{
		{"audience", binding.Audience},
		{"scope", binding.Scope},
	} {
		if part.value == "" || !singleLine(part.value) {
			return fmt.Errorf("%w: principal %q binding %q %s must be a single-line value, got %q",
				ErrInvalid, principal, binding.ID, part.label, part.value)
		}
	}
	// The queue and the namespace are optional, and shape-checked only when
	// present. Absence is a declaration — no authority on that dimension — so
	// it needs no validation; a present value that is multi-line or carries
	// control characters is a value nothing can compare reliably.
	for _, part := range []struct{ label, value string }{
		{"queue", binding.Queue},
		{"namespace", binding.Namespace},
	} {
		if part.value != "" && !singleLine(part.value) {
			return fmt.Errorf("%w: principal %q binding %q %s must be a single-line value, got %q",
				ErrInvalid, principal, binding.ID, part.label, part.value)
		}
	}
	return nil
}

// singleLine reports a printable, whitespace-free, single-line value. That is
// the shape of a name; it is not the shape of a PEM block, a wrapped token or a
// pasted credential, so the check also holds the "names identities, never
// credentials" invariant against the obvious accident.
func singleLine(value string) bool {
	return !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// ValidateAgainst holds a validated authority document against the ceiling the
// caller supplies: the envelope revision agrees, every binding the document
// grants is one the envelope holds, and the build it is approved for is one the
// envelope approved.
//
// "Inside the envelope" means EXACT element inclusion — same ID, same revision,
// and the same audience, scope, queue and namespace. It deliberately does not
// mean "some envelope binding subsumes this one". A subsumption rule is the same
// rule as searching the bindings for one that contains a set of scopes, which
// the credential contract forbids for the reason it forbids it: whoever writes
// the subsumption predicate decides what authority means, and a predicate that
// is one case too generous grants authority nobody reviewed.
//
// A tombstone claims nothing, so it is inside every envelope of its revision.
func (document *AuthorityDocument) ValidateAgainst(envelope Envelope) error {
	if err := document.Validate(); err != nil {
		return err
	}
	if envelope.Revision == 0 {
		return fmt.Errorf("%w: the envelope names no revision", ErrInvalid)
	}
	if document.EnvelopeRevision != envelope.Revision {
		return fmt.Errorf("%w: authority %q was validated against envelope revision %d, this envelope is revision %d",
			ErrOutsideEnvelope, document.Authority, document.EnvelopeRevision, envelope.Revision)
	}
	if document.Removed {
		return nil
	}
	if !slices.Contains(envelope.ApprovedBuilds, document.ApprovedBuild) {
		return fmt.Errorf("%w: authority %q is approved for build %s, which envelope revision %d has not approved",
			ErrOutsideEnvelope, document.Authority, document.ApprovedBuild, envelope.Revision)
	}
	for _, principal := range document.Principals {
		for _, binding := range principal.Bindings {
			if !slices.Contains(envelope.Bindings, binding) {
				return fmt.Errorf("%w: authority %q grants principal %q binding %q (revision %d, %s/%s on %s in %s), which envelope revision %d does not hold",
					ErrOutsideEnvelope, document.Authority, principal.Principal, binding.ID, binding.Revision,
					binding.Audience, binding.Scope, binding.Queue, binding.Namespace, envelope.Revision)
			}
		}
	}
	return nil
}

// Binding returns the unit of authority this document grants under an ID, and
// the principal that holds it. Lookup is exact and by ID: there is no variant
// that searches for a binding matching a set of scopes, because that search is
// the thing the sealed-ID contract exists to remove.
func (document *AuthorityDocument) Binding(id string) (AuthorityBinding, string, bool) {
	if document == nil {
		return AuthorityBinding{}, "", false
	}
	for _, principal := range document.Principals {
		for _, binding := range principal.Bindings {
			if binding.ID == id {
				return binding, principal.Principal, true
			}
		}
	}
	return AuthorityBinding{}, "", false
}

// CanonicalBytes returns the deterministic JSON encoding of a validated
// authority document — the payload a signature covers, and the input to Digest.
// It is the same encoding discipline as the presence document's: keys in name
// order at every depth, collections in a defined order, numbers kept as their
// exact literals.
func (document *AuthorityDocument) CanonicalBytes() ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	normalized := *document
	normalized.Principals = make([]PrincipalAuthority, 0, len(document.Principals))
	for _, principal := range document.Principals {
		bindings := slices.Clone(principal.Bindings)
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
		principal.Bindings = bindings
		normalized.Principals = append(normalized.Principals, principal)
	}
	sort.Slice(normalized.Principals, func(i, j int) bool {
		return normalized.Principals[i].Principal < normalized.Principals[j].Principal
	})
	return canonicalJSON(normalized)
}

// Digest is the SHA-256 of the canonical encoding, with the same compatibility
// property the presence digest has: two digests are comparable only when both
// came from the same encoding.
func (document *AuthorityDocument) Digest() (string, error) {
	canonical, err := document.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return digestOf(canonical), nil
}

// Activation is a matched (authority, presence, build) tuple: the one shape in
// which authority is active. It is returned by value and carries no method,
// because it is a conclusion rather than a capability — holding one proves the
// two documents lined up, and nothing more.
type Activation struct {
	// Authority is the authority document's ID.
	Authority string
	// Binding is the presence document's binding ID.
	Binding string
	// Build is the approved build both halves name.
	Build ImageDigest
	// Generation is the presence generation that activated it.
	Generation uint64
	// EnvelopeRevision is the revision both halves were validated against.
	EnvelopeRevision uint64
	// Domain is the ownership domain both halves speak for.
	Domain string
}

// AppliedAuthority is what a host durably recorded for one authority ID. It is
// the host's own state, exactly as Applied is for presence, and for the same
// reason: a generation is only replay-proof if something remembers the last one
// applied.
//
// Authority had no such record. decide gave presence a stale-generation and a
// rewritten-generation refusal and kept tombstones; authority got nothing
// equivalent, so a signed document at an older generation was indistinguishable
// from a current one.
type AppliedAuthority struct {
	// Authority is the authority ID this record is for.
	Authority string
	// Generation is the generation the host applied.
	Generation uint64
	// Digest is that generation's canonical digest, so a rewritten generation
	// is detectable rather than silently reapplied.
	Digest string
	// Binding is the presence binding the applied generation was granted over.
	// A later generation must name the same one: authority does not migrate
	// between bindings.
	Binding string
	// Domain is the ownership domain it was applied under.
	Domain string
	// Removed records that the applied generation was a tombstone.
	Removed bool
}

// AppliedAuthorityFrom builds the record a host persists after applying an
// authority document.
func AppliedAuthorityFrom(document *AuthorityDocument) (AppliedAuthority, error) {
	digest, err := document.Digest()
	if err != nil {
		return AppliedAuthority{}, err
	}
	return AppliedAuthority{
		Authority:  document.Authority,
		Generation: document.Generation,
		Digest:     digest,
		Binding:    document.PresenceBinding,
		Domain:     document.OwnershipDomain,
		Removed:    document.Removed,
	}, nil
}

// decideAuthority holds an authority document against what the host already
// applied for that authority ID. It is the presence `decide` rule, applied to
// the half that had none.
func decideAuthority(record AppliedAuthority, document *AuthorityDocument) (Decision, error) {
	if record.Authority == "" {
		// Nothing applied for this ID yet.
		return DecisionApply, nil
	}
	if record.Authority != document.Authority {
		return "", fmt.Errorf("%w: applied record is for authority %q and this document is %q",
			ErrAppliedUnusable, record.Authority, document.Authority)
	}
	if !digestPattern.MatchString(record.Digest) || record.Generation == 0 {
		return "", fmt.Errorf("%w: applied authority %q requires a generation and the digest it was applied as; build it with AppliedAuthorityFrom rather than by hand",
			ErrAppliedUnusable, record.Authority)
	}
	if !namePattern.MatchString(record.Domain) {
		return "", fmt.Errorf("%w: applied authority %q requires the ownership domain it was applied under",
			ErrAppliedUnusable, record.Authority)
	}
	if record.Removed {
		return "", fmt.Errorf("%w: authority %q was withdrawn at generation %d; a withdrawn authority is terminal and a new grant needs a new authority ID",
			ErrTombstoned, record.Authority, record.Generation)
	}
	if record.Domain != document.OwnershipDomain {
		return "", fmt.Errorf("%w: authority %q was applied under domain %q and this document declares %q",
			ErrWrongDomain, record.Authority, record.Domain, document.OwnershipDomain)
	}
	if record.Binding != document.PresenceBinding {
		return "", fmt.Errorf("%w: authority %q was applied over binding %q and this document names %q; authority does not migrate between bindings",
			ErrInvalid, record.Authority, record.Binding, document.PresenceBinding)
	}
	if document.Generation < record.Generation {
		return "", fmt.Errorf("%w: authority %q is at generation %d and this document is generation %d",
			ErrStaleGeneration, record.Authority, record.Generation, document.Generation)
	}
	if document.Generation > record.Generation {
		return DecisionApply, nil
	}
	digest, err := document.Digest()
	if err != nil {
		return "", err
	}
	if digest != record.Digest {
		return "", fmt.Errorf("%w: authority %q generation %d was applied as %s and now reads %s",
			ErrRewrittenGeneration, record.Authority, document.Generation, record.Digest, digest)
	}
	return DecisionCurrent, nil
}

// ActivationRequest is everything activation needs. It is a struct rather than
// five positional arguments because three of them were added by review and the
// next one should not change every call site again — and because a caller
// reading it can see that the envelope and the applied record are not optional.
type ActivationRequest struct {
	// Authority and Presence are the two halves, each DELIVERED — a carrier a
	// BundleVerifier accepted. Neither activates alone, and neither reaches
	// here unverified: see Delivered.
	Authority *DeliveredAuthority
	Presence  *Delivered

	// Build is the execution being asked about.
	Build ImageDigest

	// Envelope is the CURRENT ceiling. Activation checks the authority against
	// it rather than trusting that the caller remembered ValidateAgainst
	// first: narrowing an envelope must reach activation, and an ordering
	// requirement a caller can forget is not a rule.
	Envelope Envelope

	// DomainsBySigner is the host's signer policy, as Host carries it. Both
	// halves' attested signers must be allowed to speak for the domain they
	// claim, and it is REQUIRED: see Activate for why emptiness used to be a
	// mode here and is now a refusal.
	DomainsBySigner map[string][]string

	// Applied is what the host recorded for this authority ID. The zero value
	// means nothing has been applied yet, which is the first generation's
	// case; anything else is folded, so a replayed older document and a
	// rewritten generation are both refused.
	Applied AppliedAuthority
}

// Activate reports whether an authority document and a presence document form a
// matched tuple for one build, and refuses with ErrNotActivated otherwise.
//
// Neither half activates alone. That is the rule, and it is why this takes both
// documents and a build rather than offering a way to ask the question of
// either one: an authority document on its own grants nothing, because nothing
// says the build it approves is the build that is present; and a presence
// document on its own grants nothing, because nothing says anyone was granted
// authority over it. A caller that holds only one of them has its answer
// already.
//
// The build is passed in rather than read out of either document, because it is
// the thing being asked about: a host asks "is this authority active for the
// build I am actually running", and reading the build out of the document that
// approves it would make the question answer itself.
//
// Activate DOES check the envelope, against request.Envelope. It did not once,
// leaving it to the caller to call ValidateAgainst first — but an ordering
// requirement a caller can forget is not a rule, and narrowing an envelope has
// to reach activation to mean anything. This comment said the opposite of the
// code for a while, which is its own lesson about prose that outlives a change.
//
// There are two entrypoints. This one is a HOST's: it takes delivered halves
// and requires the host's signer policy. ActivateRendered is a renderer's: it
// takes parsed documents, applies no policy because there is no attestation to
// apply one to, and answers with a RenderedMatch that is not an Activation.
// One implementation, two entrypoints — the same shape as Admit and
// AdmitRendered.
func Activate(request ActivationRequest) (Activation, error) {
	// The signer policy is REQUIRED, and this is the hole that closed.
	//
	// It used to be consulted only `if len(policy) > 0`, with the field's own
	// comment saying emptiness meant a renderer pre-checking a pair. The
	// reason that is wrong is simply that it makes a SECURITY CHECK OPTIONAL
	// AND INDISTINGUISHABLE FROM ITS ABSENCE: a named host which states no
	// policy lets every signer it accepts speak for every domain it accepts,
	// and nothing in the call says whether the caller meant to waive the check
	// or forgot the field. Host.admit had already reached that conclusion
	// about the same policy one file away — "an unstated policy would let any
	// accepted signer claim any accepted domain" — and activation is the other
	// place a self-asserted domain would otherwise be taken at its word.
	//
	// AN EARLIER VERSION OF THIS COMMENT ARGUED SOMETHING FALSE, and the
	// correction matters more than the original claim because the false
	// version was load-bearing prose. It said a renderer CANNOT OBTAIN the
	// *Delivered halves this function takes, since their fields are unexported
	// and VerifyDelivered is the only constructor. module-saas-starter#953
	// refuted it in nine lines: BundleVerifier is an interface the CALLER
	// supplies, so a permissive implementation returning any identity yields a
	// *Delivered with no attestation behind it, and DeliveredBy's own comment
	// already says the signer "is a string the CALLER handed it".
	//
	// Why that mattered rather than being a pedantic correction: applied
	// consistently, the same argument says Host.admit's `host.Coordinate != ""`
	// guard is pointless too. It is not. Removing it turns SEVEN tests in this
	// package red, because AdmitRendered routes through Host{}.admit and that
	// branch is what lets a renderer run the host-free checks at all. A false
	// justification for a correct change is worse than no justification: the
	// next reader applies it one file over and removes something that works.
	//
	// What *Delivered actually buys is ORDERING WITHIN ONE CODEBASE, held by
	// the compiler: no sequence of exported calls reaches a judgement without
	// some verifier having accepted those exact bytes. That is what its type
	// comment claims and it is worth having. It is not evidence to core about
	// who signed, so it cannot carry an argument about what a caller is ABLE
	// to construct.
	//
	// A renderer that genuinely wants the match without host policy calls
	// ActivateRendered, which takes the documents a renderer actually holds.
	if len(request.DomainsBySigner) == 0 {
		return Activation{}, fmt.Errorf("%w: activation needs the host's signer policy; an unstated one would let any signer speak for any domain, and a renderer with no policy to apply wants ActivateRendered",
			ErrNotActivated)
	}
	build := request.Build
	if request.Authority == nil {
		return Activation{}, fmt.Errorf("%w: no authority document; presence alone grants nothing", ErrNotActivated)
	}
	if request.Presence == nil {
		return Activation{}, fmt.Errorf("%w: no presence document; authority alone grants nothing", ErrNotActivated)
	}
	// Both re-derived from the ATTESTED bytes. An outside-constructed
	// &DeliveredAuthority{} used to reach a nil pointer here and PANIC; it now
	// gets a refusal, because an empty payload yields no document.
	authority, err := request.Authority.Document()
	if err != nil {
		return Activation{}, fmt.Errorf("%w: the authority half does not re-derive from its attested bytes: %v", ErrNotActivated, err)
	}
	presence, err := request.Presence.Document()
	if err != nil {
		return Activation{}, fmt.Errorf("%w: the presence half does not re-derive from its attested bytes: %v", ErrNotActivated, err)
	}
	// Both halves must have been attested by a signer this host lets speak
	// for the domain they claim — the same policy Admit applies, because
	// activation is the other place a self-asserted domain would be taken at
	// its word.
	policy := request.DomainsBySigner
	for _, half := range []struct {
		what   string
		signer string
		domain string
	}{
		{"authority", request.Authority.signer, authority.OwnershipDomain},
		{"presence", request.Presence.signer, presence.OwnershipDomain},
	} {
		if !slices.Contains(policy[half.signer], half.domain) {
			return Activation{}, fmt.Errorf("%w: the %s half was signed by %q, which this host does not let speak for domain %q",
				ErrNotActivated, half.what, half.signer, half.domain)
		}
	}
	// The ceiling is the HOST's, and it is checked here rather than in the
	// shared body, because a renderer cannot answer it: ValidateAgainst tests
	// the document's own ApprovedBuild against the envelope's approved list
	// and its bindings against the envelope's, so an envelope assembled from
	// the document under check answers itself. Envelope's own doc already
	// says why that is not allowed to happen — "an envelope a document
	// carried would be a document declaring its own ceiling".
	if err := authority.ValidateAgainst(request.Envelope); err != nil {
		return Activation{}, err
	}
	return activate(authority, presence, build, request.Envelope.Revision, request.Applied)
}

// RenderedActivationRequest is the pair a RENDERER holds: parsed documents it
// is about to write, with no attestation and so no signer to apply a policy
// to.
type RenderedActivationRequest struct {
	// Authority and Presence are the two halves as parsed, NOT delivered. A
	// renderer's documents are not signed yet — signing happens at publish —
	// which is the same reason AdmitRendered exists beside Admit.
	Authority *AuthorityDocument
	Presence  *SolutionHostBinding

	// Build is the execution being asked about.
	Build ImageDigest

	// EnvelopeRevision is the revision both halves must name. It is a NUMBER
	// and not an Envelope, and that distinction is the whole reason this
	// request type exists separately.
	//
	// A renderer holds no envelope, by design on both sides: the render
	// derives an authority document from a module contract, which is a
	// REQUEST, and the platform checks it against the ceiling at apply. A
	// composition carries host.envelope_revision — a number a reviewer stamps
	// — and nothing else of the envelope. Envelope.ApprovedBuilds is the
	// platform's approval record, and at publish the renderer is PROPOSING a
	// build to it rather than reading it.
	//
	// So the only Envelope a renderer could hand this call is one assembled
	// from the document under check, which makes ValidateAgainst answer
	// itself. This type took an Envelope when it shipped, and cli#855
	// reported there was therefore no honest call available: a zero Envelope
	// is refused outright, and a derived one is the self-answering shape both
	// Activate's own comment about the build and Envelope's own doc refuse.
	// An entrypoint whose only possible caller must supply a value it can only
	// derive from the thing under check is the defect; see the named pattern
	// below.
	//
	// This was the third instance of one shape. It is named and tabulated in
	// docs/architecture.md under "A required input needs an independent
	// source", with the test it yields: name the caller, and name where that
	// caller gets this value from.
	EnvelopeRevision uint64

	// Applied is what the renderer knows was applied for this authority ID.
	// For a publish reading the base branch this is honest state, through
	// AppliedAuthorityFrom(prior) — the fold is one of the rules a renderer
	// CAN answer, so it is not "usually nothing".
	Applied AppliedAuthority
}

// RenderedMatch is what ActivateRendered answers: the two halves MATCH for one
// build. It is deliberately NOT an Activation.
//
// The distinction is the one Verified, Authenticated and Inspected carry in
// workcontext, for the same reason: the type says which question was answered.
// An Activation means a host's own signer policy admitted both halves. A
// RenderedMatch means only that the documents agree with each other — nobody
// has attested either one, so it cannot be the basis of an authorization
// decision, and it cannot be passed where an Activation is required. That is
// enforced by it being a different type rather than by a caution in prose.
type RenderedMatch struct {
	// Authority is the authority document's ID.
	Authority string
	// Binding is the presence document's binding ID.
	Binding string
	// Build is the build both halves name.
	Build ImageDigest
	// Generation is the presence generation that matched.
	Generation uint64
	// EnvelopeRevision is the revision both halves were validated against.
	EnvelopeRevision uint64
	// Domain is the ownership domain both halves speak for.
	Domain string
}

// ActivateRendered runs every activation check that does not need an
// attestation, over PARSED documents, for a renderer checking a pair it is
// about to write.
//
// It exists because requiring the signer policy in Activate would otherwise
// have left a renderer with no way to ask whether its pair matches — and the
// way that absence gets resolved in practice is a consumer re-implementing the
// tuple rules, which is the duplication this package exists to prevent. It is
// the same split, for the same reason, as AdmitRendered beside Admit: one
// implementation, two entrypoints.
//
// What it checks: both documents validate, the build is a digest, both halves
// name the envelope revision the caller gave, the generation fold against
// Applied, the target binding, withdrawal of either half, the host and domain
// agreeing, the approved build, the presence naming that build, and the
// effective-from generation.
//
// What it CANNOT check, and neither is a gap because neither is answerable
// without host state:
//
//   - WHO SIGNED either half. A renderer's documents carry no attestation, so
//     there is no signer to hold a policy against.
//   - WHETHER THE AUTHORITY FITS THE CEILING. ValidateAgainst needs the
//     platform's envelope — the bindings an administrator wrote and the
//     builds it has approved — and a renderer has neither. Taking an
//     Envelope here would only invite one derived from the document under
//     check, which answers itself. The revision is the one part of the
//     envelope a renderer legitimately holds, so that is the part this takes.
//
// Both omissions are the same split AdmitRendered already makes, and the
// result type carries it: a RenderedMatch is not an Activation and no
// sequence of calls turns one into the other.
func ActivateRendered(request RenderedActivationRequest) (RenderedMatch, error) {
	if request.Authority == nil {
		return RenderedMatch{}, fmt.Errorf("%w: no authority document; presence alone grants nothing", ErrNotActivated)
	}
	if request.Presence == nil {
		return RenderedMatch{}, fmt.Errorf("%w: no presence document; authority alone grants nothing", ErrNotActivated)
	}
	activation, err := activate(request.Authority, request.Presence, request.Build, request.EnvelopeRevision, request.Applied)
	if err != nil {
		return RenderedMatch{}, err
	}
	return RenderedMatch(activation), nil
}

// activate is the shared tuple check both entrypoints run. Everything that
// needs an attestation is done by the caller, and everything that does not is
// here exactly once.
func activate(authority *AuthorityDocument, presence *SolutionHostBinding, build ImageDigest, envelopeRevision uint64, applied AppliedAuthority) (Activation, error) {
	if err := authority.Validate(); err != nil {
		return Activation{}, err
	}
	if err := presence.Validate(); err != nil {
		return Activation{}, err
	}
	if !digestPattern.MatchString(string(build)) {
		return Activation{}, fmt.Errorf("%w: %q is not a SHA-256 OCI image manifest digest", ErrNotActivated, build)
	}
	// Both halves must name the revision the CALLER named, which is stronger
	// than the rule this replaced. It used to be that the two halves agreed
	// with EACH OTHER, which two documents stamped against a superseded
	// ceiling satisfy between themselves. Naming the revision is the one part
	// of the envelope a renderer holds honestly, so it is the part both
	// entrypoints check.
	if envelopeRevision == 0 {
		return Activation{}, fmt.Errorf("%w: no envelope revision was named, and a tuple that agrees with itself about no ceiling activates nothing", ErrNotActivated)
	}
	for _, half := range []struct {
		what  string
		named uint64
	}{
		{"authority " + authority.Authority, authority.EnvelopeRevision},
		{"binding " + presence.Binding, presence.EnvelopeRevision},
	} {
		if half.named != envelopeRevision {
			return Activation{}, fmt.Errorf("%w: %s was validated against envelope revision %d and this is revision %d",
				ErrNotActivated, half.what, half.named, envelopeRevision)
		}
	}
	// The generation fold. A signed document at an older generation, or a
	// rewritten one, is refused — and a withdrawn authority is terminal.
	if _, err := decideAuthority(applied, authority); err != nil {
		return Activation{}, err
	}
	// The TARGET. Without this an authority document activated any binding in
	// the same host and domain running the same image.
	if authority.PresenceBinding != presence.Binding {
		return Activation{}, fmt.Errorf("%w: authority %q is granted over binding %q and this is binding %q",
			ErrNotActivated, authority.Authority, authority.PresenceBinding, presence.Binding)
	}
	if authority.Removed {
		return Activation{}, fmt.Errorf("%w: authority %q is withdrawn at generation %d", ErrNotActivated, authority.Authority, authority.Generation)
	}
	if presence.Removed {
		return Activation{}, fmt.Errorf("%w: binding %q is withdrawn at generation %d", ErrNotActivated, presence.Binding, presence.Generation)
	}
	if authority.Host != presence.Host {
		return Activation{}, fmt.Errorf("%w: authority %q grants on %s/%s and binding %q is present on %s/%s",
			ErrNotActivated, authority.Authority, authority.Host.Coordinate, authority.Host.Component,
			presence.Binding, presence.Host.Coordinate, presence.Host.Component)
	}
	if authority.OwnershipDomain != presence.OwnershipDomain {
		return Activation{}, fmt.Errorf("%w: authority %q speaks for domain %q and binding %q for %q",
			ErrNotActivated, authority.Authority, authority.OwnershipDomain, presence.Binding, presence.OwnershipDomain)
	}
	if authority.ApprovedBuild != build {
		return Activation{}, fmt.Errorf("%w: authority %q is approved for build %s, not %s",
			ErrNotActivated, authority.Authority, authority.ApprovedBuild, build)
	}
	// The presence document must name the build as one of its workloads'. This
	// is the acceptance case: authority for build B against a presence entry
	// naming build A activates nothing, however sound each document is.
	if !slices.Contains(presence.Builds(), build) {
		return Activation{}, fmt.Errorf("%w: binding %q at generation %d runs builds %v and not %s",
			ErrNotActivated, presence.Binding, presence.Generation, presence.Builds(), build)
	}
	if presence.Generation < authority.EffectiveFrom {
		return Activation{}, fmt.Errorf("%w: authority %q is effective from generation %d and binding %q is at %d",
			ErrNotActivated, authority.Authority, authority.EffectiveFrom, presence.Binding, presence.Generation)
	}
	return Activation{
		Authority:        authority.Authority,
		Binding:          presence.Binding,
		Build:            build,
		Generation:       presence.Generation,
		EnvelopeRevision: presence.EnvelopeRevision,
		Domain:           presence.OwnershipDomain,
	}, nil
}
