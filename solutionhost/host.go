package solutionhost

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/codefly-dev/core/composition"
)

// reservedOwner is composition.ValidateCollisions' sentinel: a claim owned by
// "base" is exempt from the reserved-namespace check. Host.Admit passes binding
// IDs as claim owners, so Validate refuses a binding called "base" rather than
// letting one inherit the exemption.
const reservedOwner = "base"

var (
	// ErrStaleGeneration means a document declares a generation the host has
	// already moved past. It is rejected, never merged into the applied one:
	// merging would produce a state no generation ever described.
	ErrStaleGeneration = errors.New("solution host binding generation is older than the applied generation")

	// ErrRewrittenGeneration means a generation the host already applied came
	// back with different contents. Generations are immutable; a rewrite is how
	// a compromised or confused writer would change what runs without a host
	// ever seeing a new generation number.
	ErrRewrittenGeneration = errors.New("solution host binding generation was rewritten")

	// ErrWrongHost means the document targets a coordinate this host does not
	// reconcile.
	ErrWrongHost = errors.New("solution host binding names a different host")

	// ErrWrongDomain means a delivery reached outside its ownership domain: a
	// set that straddles two domains, or a document that would change or remove
	// a binding an earlier generation applied under a different one.
	//
	// This is what makes the ownership domain an authority rather than a label.
	// Without it, "the delivered set within domain D is exactly desired" is a
	// rule any delivery could satisfy for any D it chose to write, so a
	// module-scoped delivery could tombstone every binding on the host by
	// claiming their domain.
	ErrWrongDomain = errors.New("solution host binding reaches outside its ownership domain")

	// ErrAppliedUnusable is returned when the host's OWN durable record of an
	// applied generation cannot be held a document against. It is distinct
	// from ErrInvalid because they accuse different parties, and the wrong
	// accusation sends the reader to the wrong repository.
	//
	// A host consumer paid for this distinction. A host that had not
	// yet persisted Applied.Domain got "solution host document is invalid:
	// applied binding "X" requires the ownership domain it was applied
	// under" — which blames a delivered document, names a binding that is
	// usually not the one being debugged, and never crashes. Applied state is
	// read once for the whole set, so a single unusable record withholds
	// EVERY binding on every pass, indefinitely, while the error points at
	// delivery.
	//
	// The remedy is the host's and only the host's: fix what it stored, or
	// discard its applied records and re-admit the delivered set. No delivery
	// can resolve it, which is exactly why it must not read as a delivery
	// problem.
	ErrAppliedUnusable = errors.New("solution host applied record is unusable; the host's own stored state must be repaired")

	// ErrTombstoned is returned when a binding or an authority has been
	// withdrawn and something tries to reapply it.
	//
	// A tombstone is TERMINAL. decide used to let any higher generation
	// resurrect a tombstoned binding ID, and nothing tested it — so everything
	// keyed on that ID (installations, operation bindings, team grants) would
	// re-attach to a new instance that merely reused the name. The ID is the
	// handle every other system holds, so reuse is indistinguishable from
	// continuity, which is the one thing a withdrawal is supposed to make
	// distinguishable.
	//
	// A replacement instance needs a NEW ID. That is a rename at the
	// renderer, not a loss: a binding ID is derived from the instance, and a
	// genuinely new instance has a genuinely new one.
	ErrTombstoned = errors.New("solution host binding was withdrawn; a tombstone is terminal and a replacement needs a new ID")
)

// Decision is what a host should do with a document it has just read.
type Decision string

const (
	// DecisionApply means the generation is newer than the applied one and its
	// checks passed.
	DecisionApply Decision = "apply"

	// DecisionCurrent means this exact generation is already applied. A host
	// re-reads its mounted document on every reconcile pass, so "unchanged" has
	// to be an answer rather than an error; only a rewrite of an applied
	// generation is an error.
	DecisionCurrent Decision = "current"
)

// Applied is what a host durably recorded for one binding ID. It is the host's
// own state, not part of any document: a runtime cannot produce it, and a
// delivery document cannot assert it.
type Applied struct {
	// Binding is the binding ID this record is for.
	Binding string
	// Generation is the generation the host applied.
	Generation uint64
	// Digest is that generation's SolutionHostBinding.Digest, so a rewritten
	// generation is detectable rather than silently reapplied.
	Digest string
	// Domain is the ownership domain the applied generation declared. A later
	// generation for this binding must declare the same one: the domain is what
	// says who may change this record, so a delivery that arrives under
	// another domain is refused rather than allowed to take the binding over.
	Domain string
	// Routes are the aliases that generation holds. They stay held until a
	// later generation releases them or a tombstone withdraws them.
	Routes []string
	// Removed records that the applied generation was a tombstone. A tombstone
	// holds no alias, and is not an absence: the binding ID and its generation
	// remain, so a late or replayed older generation is still rejected.
	Removed bool
}

// AppliedFrom builds the record a host persists after applying a document.
func AppliedFrom(document *SolutionHostBinding) (Applied, error) {
	digest, err := document.Digest()
	if err != nil {
		return Applied{}, err
	}
	return Applied{
		Binding:    document.Binding,
		Generation: document.Generation,
		Digest:     digest,
		Domain:     document.OwnershipDomain,
		Routes:     document.Aliases(),
		Removed:    document.Removed,
	}, nil
}

// Host is what a host has applied and what it reserves. It carries no
// behaviour of its own: reconciliation, provenance verification and durability
// belong to the host, and this type only answers whether a document may be
// applied on top of the state the host reports.
//
// The zero value, with an empty Coordinate and nothing applied, is what a
// renderer uses to check a set it is about to write: every check that does not
// need host state still runs.
type Host struct {
	// Coordinate is the host this state belongs to. When set, a document
	// naming a different coordinate is refused. A renderer checking a set
	// before delivery leaves it empty.
	Coordinate string

	// Reserved are route namespaces no binding may claim — "codefly" reserves
	// "codefly", "codefly/admin" and "codefly.admin".
	Reserved []string

	// Domains are the ownership domains this host accepts delivery from.
	// Required whenever Coordinate is set, and not consulted when it is not —
	// a renderer pre-checking a set it is about to write has no host to accept
	// on behalf of.
	//
	// It exists because the applied record cannot bound a binding's FIRST
	// generation: there is nothing to compare a domain against yet, so without
	// this any delivery could claim any unseen binding ID under any domain it
	// chose to write and own it from then on. The host declaring which domains
	// it accepts is what closes that, and it is the host's to declare because
	// core cannot know which delivery is entitled to a name it has never seen.
	Domains []string

	// DomainsBySigner is the host's policy: which ownership domains each
	// SIGNER IDENTITY may deliver under. Required whenever Coordinate is set.
	//
	// Domains alone was not enough, and the gap is the keyless form of the
	// thing this whole design refuses at the key level. A document ASSERTS its
	// own ownership domain, so any signer the host accepted at all could write
	// any domain in Domains and take over bindings in it — "a document
	// nominates its own authority", one layer up from the key. Core cannot
	// establish who a signer is (that is the caller's trust root, through
	// BundleVerifier) but it can refuse a document whose attested signer is
	// not one this host lets speak for that domain.
	//
	// It is host CONFIGURATION core consumes, not a trust root core resolves:
	// the caller supplies the verified identity, and this says what that
	// identity is allowed to deliver.
	DomainsBySigner map[string][]string

	// Applied is the host's durable record, at most one entry per binding ID.
	Applied []Applied
}

// Admission is what Admit concluded about one document: the decision a host
// should act on, or the reason that document alone was refused.
type Admission struct {
	// Binding is the document's binding ID. It is empty when the document did
	// not parse far enough to name one.
	Binding string
	// Decision is what the host should do, and is set only when Err is nil.
	Decision Decision
	// Err is why this document was refused, or nil. It carries the same
	// sentinels Admit returns, so errors.Is works on it.
	Err error
}

// Admit checks one desired set against this host's state and returns one
// Admission per document, in the order given.
//
// The set is checked as a whole because two of the rules are not properties of
// a document: a route alias is unique within a host, and a generation is only
// meaningful against the one already applied. A renderer runs the same call on
// what it is about to write, so a collision is refused where it was authored
// instead of leaving the host to guess which of two claimants meant it.
//
// Validity, though, is per document, so one malformed binding does not freeze
// every other binding on the host: each refusal lands on its own Admission and
// the rest still carry a decision. The returned error is non-nil whenever any
// document was refused, so a caller that checks only the error still applies
// nothing; a caller that wants to apply what is sound reads the Admissions.
//
// Route aliases are unique within ONE host, so they are compared per
// coordinate. That matters for the renderer, whose set legitimately spans every
// host a product delivers to: the same alias on two coordinates is not a
// collision.
//
// Admit decides admission only. It does not verify who signed the document or
// how it arrived; a host still checks provenance and its expected target first.
// Admit takes DELIVERED documents — ones whose carrier a BundleVerifier
// accepted — rather than parsed ones. See Delivered: "verified" used to be
// part of a function name and nothing more, and a host that forgot to verify a
// carrier had no way to find out.
func (host Host) Admit(delivered ...*Delivered) ([]Admission, error) {
	// A HOST IS NAMED, and this is the hole that closed.
	//
	// Every provenance check in admit is guarded by `host.Coordinate != ""`,
	// because the zero Host is how AdmitRendered reaches the checks that need
	// no host state. But Host{} is also constructible by any caller, and
	// Host{}.Admit then returned DecisionApply for a document from an unlisted
	// signer, under an unlisted domain, targeting a foreign coordinate — every
	// provenance rule skipped, with an attestation making it look checked.
	//
	// The zero-host path is still there and still correct; it just is not
	// reachable through the public entrypoint any more. AdmitRendered calls
	// the internal admit directly, which is why those eight tests still stand.
	if host.Coordinate == "" {
		return nil, fmt.Errorf("%w: a host admits documents under its own coordinate; an unnamed host skips every provenance check, and a renderer with no host state wants AdmitRendered",
			ErrInvalid)
	}
	documents := make([]*SolutionHostBinding, len(delivered))
	signers := make([]string, len(delivered))
	for index, one := range delivered {
		if one == nil {
			return nil, fmt.Errorf("%w: delivered document %d is nil", ErrInvalid, index)
		}
		// Re-derived from the ATTESTED bytes, not taken from anything a caller
		// has held. See Delivered.
		document, err := one.Document()
		if err != nil {
			return nil, err
		}
		documents[index], signers[index] = document, one.signer
	}
	return host.admit(documents, signers)
}

// RenderedAdmission is what AdmitRendered answers, and it is deliberately NOT
// an Admission.
//
// AdmitRendered returned Admission, the same type a HOST's Admit answers, so
// "this set is internally consistent" and "this host admits these documents"
// were the same value — C4's fail-open under another name. A caller holding
// one could pass it where the other was meant, and a reviewer reading a
// function that takes an Admission could not tell which it was given.
//
// The distinction is the one Verified, Authenticated and Inspected carry in
// workcontext, for the same reason: the type says which question was answered.
type RenderedAdmission struct {
	// Binding is the document's binding ID, empty when it did not parse far
	// enough to name one.
	Binding string

	// Decision is what a host WOULD do on the host-free rules alone, and is
	// set only when Err is nil. It is not a host's decision: no coordinate, no
	// domain policy, no signer policy and no applied state were consulted.
	Decision Decision

	// Err is why this document was refused, or nil, carrying the same
	// sentinels so errors.Is works on it.
	Err error

	// Fold is the generation decision against the applied record the caller
	// supplied, when it supplied one. See AdmitRendered.
	Fold Decision
}

// RenderedSet is one parsed document plus what the renderer knows was applied
// for its binding — the inputs a publish holds from the base branch.
type RenderedSet struct {
	// Document is the parsed presence document, not delivered: a renderer's
	// documents are not signed yet.
	Document *SolutionHostBinding

	// Applied is the presence record for the SAME binding, or the zero value
	// with FirstRecord set.
	Applied Applied

	// FirstRecord states explicitly that no record exists for this binding.
	// A zero record with no marker is refused rather than folded, for the
	// reason ActivationRequest states it per half.
	FirstRecord bool
}

// AdmitRenderedSets runs the host-free checks AND THE GENERATION FOLD over
// parsed documents, for a renderer checking a set it is about to write.
//
// It exists because AdmitRendered takes no Host and therefore no applied
// records, so it ran no fold at all — and ActivateRendered folds presence only
// as half of a pair, so a module with presence and no contract had no
// entrypoint for it. A renderer consumer reported that it was restating the
// domain-continuity rule in its own code as a result, which is the duplication
// this package exists to prevent: the gap was in this surface, not in their
// reading of it.
//
// What it checks beyond AdmitRendered: for each document whose caller supplied
// a record, the generation fold through decide — stale, rewritten and
// tombstoned all refused — plus that the record is for THAT binding, is well
// formed, and was applied under the same ownership domain. That last one is
// the rule the consumer was duplicating.
//
// What it still cannot check, for the reason AdmitRendered cannot: a
// coordinate, the domains a host accepts, or who may speak for one. Those need
// host state, and the answer carries RenderedAdmission rather than Admission
// so it cannot be mistaken for a host's.
func AdmitRenderedSets(sets ...RenderedSet) ([]RenderedAdmission, error) {
	documents := make([]*SolutionHostBinding, len(sets))
	for index, set := range sets {
		if set.Document == nil {
			return nil, fmt.Errorf("%w: rendered set %d carries no document", ErrInvalid, index)
		}
		documents[index] = set.Document
	}
	base, err := Host{}.admit(documents, make([]string, len(documents)))
	if err != nil {
		return nil, err
	}
	out := make([]RenderedAdmission, len(base))
	for index, admission := range base {
		out[index] = RenderedAdmission{
			Binding:  admission.Binding,
			Decision: admission.Decision,
			Err:      admission.Err,
		}
		if out[index].Err != nil {
			continue
		}
		set := sets[index]
		given := set.Applied.Binding != ""
		if given == set.FirstRecord {
			out[index].Err = fmt.Errorf("%w: binding %q must either carry its applied record or declare that none exists",
				ErrAppliedUnusable, set.Document.Binding)
			continue
		}
		if !given {
			out[index].Fold = DecisionApply
			continue
		}
		if set.Applied.Binding != set.Document.Binding {
			out[index].Err = fmt.Errorf("%w: the applied record is for binding %q and this document is %q",
				ErrAppliedUnusable, set.Applied.Binding, set.Document.Binding)
			continue
		}
		if set.Applied.Generation == 0 || !digestPattern.MatchString(set.Applied.Digest) {
			out[index].Err = fmt.Errorf("%w: applied record for binding %q needs a generation and the digest it was applied as; build it with AppliedFrom rather than by hand",
				ErrAppliedUnusable, set.Applied.Binding)
			continue
		}
		// DOMAIN CONTINUITY — the rule a renderer consumer was restating
		// because this entrypoint did not exist. The applied record's domain
		// is what says who may change this binding, so a document arriving
		// under another domain is refused whatever its generation.
		if set.Applied.Domain != "" && set.Applied.Domain != set.Document.OwnershipDomain {
			out[index].Err = fmt.Errorf("%w: binding %q was applied under domain %q and this document declares %q",
				ErrWrongDomain, set.Applied.Binding, set.Applied.Domain, set.Document.OwnershipDomain)
			continue
		}
		fold, err := decide(set.Applied, set.Document)
		if err != nil {
			out[index].Err = err
			continue
		}
		out[index].Fold = fold
	}
	return out, nil
}

// AdmitRendered runs the checks that need no host state, over PARSED
// documents, for a renderer checking a set it is about to write.
//
// It exists because making Admit take *Delivered broke the renderer, and the
// break was invisible from inside core: a renderer's documents are not signed
// yet — signing happens at publish, and a --local qualification publish is
// never signed — so there is no carrier for a BundleVerifier to accept and no
// way to reach the zero-host checks at all. A consumer reported it by starting
// to re-implement them, which is the failure this package exists to end
// appearing in the fix for it. One implementation, two entrypoints.
//
// It takes NO Host, deliberately and not as a convenience. A Host carries
// applied state, a coordinate and a signer policy, and none of those can be
// checked without an attestation — so a signature cannot be the thing a
// renderer forgets, because there is nothing here to forget it for. The
// invariant stands exactly as before: no sequence of calls reaches a HOST's
// Admit without an attestation having held.
//
// What it checks, which is every rule that does not need host state: each
// document validates, no binding is declared twice in one set, no two
// documents claim the same route alias, and each document's generation is
// decided against nothing applied. What it cannot check is anything about a
// host — a coordinate, a domain the host accepts, who may speak for it, or a
// generation against an applied record. A renderer pre-checking a set has no
// host to answer those for.
func AdmitRendered(documents ...*SolutionHostBinding) ([]RenderedAdmission, error) {
	// No signers: the signer policy is only consulted for a named host, and
	// there is none here.
	//
	// It answers RenderedAdmission and not Admission, which was C4's
	// fail-open under another name: the same type a host's Admit returns made
	// "this set is consistent" and "this host admits these" one value.
	sets := make([]RenderedSet, len(documents))
	for index, document := range documents {
		sets[index] = RenderedSet{Document: document, FirstRecord: true}
	}
	return AdmitRenderedSets(sets...)
}

func (host Host) admit(documents []*SolutionHostBinding, signers []string) ([]Admission, error) {
	applied, err := host.appliedByBinding()
	if err != nil {
		return nil, err
	}
	// Applied state is one host's durable record and carries no coordinate of
	// its own, so it is only interpretable against a named host. Without this,
	// a caller mixing applied state into a coordinate-less check would have its
	// aliases silently compared against documents for other hosts.
	// Defence in depth on the INTERNAL entrypoint. Admit now refuses an
	// unnamed host outright, so this is unreachable through the public
	// surface; AdmitRendered passes a zero Host and never passes applied
	// state. It stays because admit is shared and a third caller would
	// otherwise inherit the hole Admit just closed.
	if len(host.Applied) != 0 && host.Coordinate == "" {
		return nil, fmt.Errorf("%w: applied state belongs to a named host, so Host.Coordinate is required", ErrAppliedUnusable)
	}
	// A named host that accepts no stated domain would accept every one, which
	// is the hole Domains exists to close — so an unset list is an error rather
	// than a permissive default. A renderer leaves both empty and is unaffected.
	if host.Coordinate != "" && len(host.Domains) == 0 {
		return nil, fmt.Errorf("%w: host %q must declare the ownership domains it accepts; an unstated list would accept every domain",
			ErrInvalid, host.Coordinate)
	}
	// Same reasoning one axis over: an unstated signer policy would let every
	// accepted signer speak for every accepted domain, which is the hole
	// DomainsBySigner exists to close.
	if host.Coordinate != "" && len(host.DomainsBySigner) == 0 {
		return nil, fmt.Errorf("%w: host %q must declare which signer identities may deliver under which domains; an unstated policy would let any accepted signer claim any accepted domain",
			ErrInvalid, host.Coordinate)
	}
	for _, domain := range host.Domains {
		if !namePattern.MatchString(domain) {
			return nil, fmt.Errorf("%w: host %q accepts invalid ownership domain %q", ErrInvalid, host.Coordinate, domain)
		}
	}

	admissions := make([]Admission, len(documents))
	declared := make(map[string]int, len(documents))
	for index, document := range documents {
		if err := document.Validate(); err != nil {
			admissions[index].Err = err
			continue
		}
		admissions[index].Binding = document.Binding
		if host.Coordinate != "" && document.Host.Coordinate != host.Coordinate {
			admissions[index].Err = fmt.Errorf("%w: binding %q targets %q, this host is %q", ErrWrongHost, document.Binding, document.Host.Coordinate, host.Coordinate)
			continue
		}
		// What the host accepts at all, which is the only thing that bounds a
		// binding's first generation.
		if host.Coordinate != "" && !slices.Contains(host.Domains, document.OwnershipDomain) {
			admissions[index].Err = fmt.Errorf("%w: binding %q is delivered under domain %q, which host %q does not accept",
				ErrWrongDomain, document.Binding, document.OwnershipDomain, host.Coordinate)
			continue
		}
		// And WHO may speak for that domain. The document asserts its own
		// domain, so without this any signer the host accepted at all could
		// write any accepted domain and take over bindings in it.
		if host.Coordinate != "" && !slices.Contains(host.DomainsBySigner[signers[index]], document.OwnershipDomain) {
			admissions[index].Err = fmt.Errorf("%w: binding %q is delivered under domain %q by signer %q, which host %q does not let speak for it",
				ErrWrongDomain, document.Binding, document.OwnershipDomain, signers[index], host.Coordinate)
			continue
		}
		// The applied record's domain is what says who may change this binding.
		// A document arriving under another domain is refused whatever its
		// generation — including a tombstone, which is the case that matters:
		// otherwise any delivery the host accepts at all could withdraw any
		// binding by declaring a higher generation under its own domain.
		if record, exists := applied[document.Binding]; exists && record.Domain != document.OwnershipDomain {
			admissions[index].Err = fmt.Errorf("%w: binding %q was applied under domain %q and this document declares %q",
				ErrWrongDomain, document.Binding, record.Domain, document.OwnershipDomain)
			continue
		}
		// One desired set declares one generation per binding: two would make
		// the applied generation depend on the order the host read them in.
		if first, exists := declared[document.Binding]; exists {
			admissions[index].Err = fmt.Errorf("%w: binding %q is declared twice in one set, at documents %d and %d", ErrInvalid, document.Binding, first, index)
			continue
		}
		declared[document.Binding] = index

		decision, err := decide(applied[document.Binding], document)
		if err != nil {
			admissions[index].Err = err
			continue
		}
		// The host's reserved namespaces constrain what delivery is asking for
		// now, so they are checked against this document's own claims and
		// nothing else. Re-judging an alias an earlier generation already holds
		// would let a newly reserved namespace refuse every unrelated binding
		// on the host until an operator tombstoned the incumbent.
		if err := composition.ValidateCollisions(routeClaims(document.Binding, document.Aliases()), host.Reserved); err != nil {
			admissions[index].Err = err
			continue
		}
		admissions[index].Decision = decision
	}
	host.refuseAliasCollisions(documents, admissions)
	for index, admission := range admissions {
		if admission.Err != nil {
			return admissions, fmt.Errorf("document %d: %w", index, admission.Err)
		}
	}
	return admissions, nil
}

// refuseAliasCollisions refuses documents whose aliases are not free, one at a
// time until the set is stable.
//
// It reruns rather than deciding the whole set at once because refusing a
// document changes the question: the generation that document would have
// replaced is not replaced after all, so it keeps the aliases the set had
// assumed it was releasing. Deciding against the first snapshot would hand one
// of those aliases to another document and collide at apply time.
func (host Host) refuseAliasCollisions(documents []*SolutionHostBinding, admissions []Admission) {
	for range documents {
		if !host.refuseOneAliasCollision(documents, admissions) {
			return
		}
	}
}

func (host Host) refuseOneAliasCollision(documents []*SolutionHostBinding, admissions []Admission) bool {
	replaced := make(map[string]struct{}, len(documents))
	byCoordinate := make(map[string][]int, len(documents))
	for index, document := range documents {
		if admissions[index].Err != nil {
			continue
		}
		replaced[document.Binding] = struct{}{}
		byCoordinate[document.Host.Coordinate] = append(byCoordinate[document.Host.Coordinate], index)
	}
	for _, coordinate := range slices.Sorted(maps.Keys(byCoordinate)) {
		indexes := byCoordinate[coordinate]
		// Binding-ID order, not the caller's argument order, so the same set
		// yields the same answer however it was assembled.
		sort.Slice(indexes, func(i, j int) bool {
			return documents[indexes[i]].Binding < documents[indexes[j]].Binding
		})
		var held []composition.Claim
		// An applied binding the set does not replace keeps its aliases; one it
		// does replace releases them, because the document in hand is that
		// binding's whole desired state. Applied state exists only for
		// host.Coordinate, which the guard in Admit establishes.
		if coordinate == host.Coordinate {
			for _, record := range host.Applied {
				if _, exists := replaced[record.Binding]; exists || record.Removed {
					continue
				}
				held = append(held, routeClaims(record.Binding, record.Routes)...)
			}
		}
		for _, index := range indexes {
			document := documents[index]
			next := append(slices.Clone(held), routeClaims(document.Binding, document.Aliases())...)
			// Uniqueness only: the reserved-namespace policy was already
			// applied to each document's own claims above.
			if err := composition.ValidateCollisions(next, nil); err != nil {
				admissions[index].Decision = ""
				admissions[index].Err = err
				return true
			}
			held = next
		}
	}
	return false
}

// OneDelivery reports whether a set of documents is ONE delivery: every
// document in it speaks for the same ownership domain. It answers
// ErrWrongDomain when the set straddles two, naming them.
//
// It is a separate call and deliberately NOT part of Host.Admit, because
// "delivered set" and "the documents a host can see" are not the same set and
// the difference decides the answer:
//
//   - A renderer writing one delivery calls this on the set it is about to
//     write. A set that straddles two domains cannot satisfy "within D the
//     delivered set is exactly desired" for any single D, so removal within it
//     is not expressible and the delivery is refused where it was authored.
//   - A host does NOT call it on its mount. A host's mount is the union of
//     however many deliveries reached it, so it legitimately carries one domain
//     per delivery; refusing that would refuse the normal case the moment a
//     second module delivered to the same host.
//
// A host does NOT infer removal from absence either, in a domain or anywhere
// else. Removal is a tombstone generation, full stop. Absence-as-removal was
// briefly documented here and was wrong twice over: a set assembled from a
// mount silently omits every document that failed to parse, so one malformed
// document beside a sound one would read as a withdrawal of the sound one; and
// nothing in a delivered set establishes that it is COMPLETE, so "absent at a
// higher generation" has no higher generation to be absent at. The tombstone
// exists precisely so that an unreadable mount, a half-synced tree or a
// delivery that simply does not cover a binding can never be read as "withdraw
// it".
//
// Documents that do not validate are skipped: an unreadable domain is that
// document's own refusal, and letting it decide the set's would turn one
// malformed document into a withheld delivery.
func OneDelivery(documents ...*SolutionHostBinding) error {
	domains := make([]string, 0, 1)
	for _, document := range documents {
		if document == nil || document.Validate() != nil {
			continue
		}
		if !slices.Contains(domains, document.OwnershipDomain) {
			domains = append(domains, document.OwnershipDomain)
		}
	}
	if len(domains) < 2 {
		return nil
	}
	slices.Sort(domains)
	return fmt.Errorf("%w: this set declares domains %v; one delivery speaks for one domain", ErrWrongDomain, domains)
}

func routeClaims(binding string, aliases []string) []composition.Claim {
	// Route-alias uniqueness within a host, and the host's reserved
	// namespaces, are exactly composition's collision vocabulary, so the same
	// checker and the same composition.ErrCollision answer here.
	claims := make([]composition.Claim, 0, len(aliases))
	for _, alias := range aliases {
		claims = append(claims, composition.Claim{Kind: composition.CollisionRoute, Key: alias, Owner: binding})
	}
	return claims
}

func decide(record Applied, document *SolutionHostBinding) (Decision, error) {
	// A tombstoned binding is terminal: no later generation reapplies it. See
	// ErrTombstoned for why reuse of the ID is the problem rather than the
	// generation ordering.
	if record.Removed && document.Generation > record.Generation {
		return "", fmt.Errorf("%w: binding %q was withdrawn at generation %d and this document is generation %d",
			ErrTombstoned, record.Binding, record.Generation, document.Generation)
	}
	if record.Binding == "" {
		return DecisionApply, nil
	}
	if document.Generation < record.Generation {
		return "", fmt.Errorf("%w: binding %q declares generation %d, applied is %d", ErrStaleGeneration, document.Binding, document.Generation, record.Generation)
	}
	if document.Generation > record.Generation {
		return DecisionApply, nil
	}
	digest, err := document.Digest()
	if err != nil {
		return "", err
	}
	if digest != record.Digest {
		return "", fmt.Errorf("%w: binding %q generation %d was applied as %s and now reads %s", ErrRewrittenGeneration, document.Binding, document.Generation, record.Digest, digest)
	}
	return DecisionCurrent, nil
}

func (host Host) appliedByBinding() (map[string]Applied, error) {
	byBinding := make(map[string]Applied, len(host.Applied))
	aliases := make(map[string]string, len(host.Applied))
	for _, record := range host.Applied {
		if !bindingPattern.MatchString(record.Binding) || record.Binding == reservedOwner {
			return nil, fmt.Errorf("%w: applied binding ID %q is invalid", ErrAppliedUnusable, record.Binding)
		}
		if record.Generation == 0 || !digestPattern.MatchString(record.Digest) {
			return nil, fmt.Errorf("%w: applied binding %q requires a generation and the digest it was applied as; build it with AppliedFrom rather than by hand",
				ErrAppliedUnusable, record.Binding)
		}
		// A record with no domain is a record no document can be held against:
		// the domain check below would pass for any domain a delivery chose.
		if !namePattern.MatchString(record.Domain) {
			return nil, fmt.Errorf("%w: applied binding %q requires the ownership domain it was applied under, got %q; a host upgrading to presence v2 persists this column, and AppliedFrom fills it",
				ErrAppliedUnusable, record.Binding, record.Domain)
		}
		if _, exists := byBinding[record.Binding]; exists {
			return nil, fmt.Errorf("%w: applied binding %q is recorded twice", ErrAppliedUnusable, record.Binding)
		}
		if record.Removed && len(record.Routes) != 0 {
			return nil, fmt.Errorf("%w: applied binding %q is a tombstone and holds no route", ErrAppliedUnusable, record.Binding)
		}
		for _, alias := range record.Routes {
			if !namePattern.MatchString(alias) {
				return nil, fmt.Errorf("%w: applied binding %q holds invalid route alias %q", ErrAppliedUnusable, record.Binding, alias)
			}
			if owner, exists := aliases[alias]; exists {
				return nil, fmt.Errorf("%w: applied route alias %q is held by both %q and %q", composition.ErrCollision, alias, owner, record.Binding)
			}
			aliases[alias] = record.Binding
		}
		record.Routes = slices.Clone(record.Routes)
		byBinding[record.Binding] = record
	}
	return byBinding, nil
}
