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
func (host Host) Admit(documents ...*SolutionHostBinding) ([]Admission, error) {
	applied, err := host.appliedByBinding()
	if err != nil {
		return nil, err
	}
	// Applied state is one host's durable record and carries no coordinate of
	// its own, so it is only interpretable against a named host. Without this,
	// a caller mixing applied state into a coordinate-less check would have its
	// aliases silently compared against documents for other hosts.
	if len(host.Applied) != 0 && host.Coordinate == "" {
		return nil, fmt.Errorf("%w: applied state belongs to a named host, so Host.Coordinate is required", ErrInvalid)
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
			return nil, fmt.Errorf("%w: applied binding ID %q is invalid", ErrInvalid, record.Binding)
		}
		if record.Generation == 0 || !digestPattern.MatchString(record.Digest) {
			return nil, fmt.Errorf("%w: applied binding %q requires a generation and the digest it was applied as", ErrInvalid, record.Binding)
		}
		if _, exists := byBinding[record.Binding]; exists {
			return nil, fmt.Errorf("%w: applied binding %q is recorded twice", ErrInvalid, record.Binding)
		}
		if record.Removed && len(record.Routes) != 0 {
			return nil, fmt.Errorf("%w: applied binding %q is a tombstone and holds no route", ErrInvalid, record.Binding)
		}
		for _, alias := range record.Routes {
			if !namePattern.MatchString(alias) {
				return nil, fmt.Errorf("%w: applied binding %q holds invalid route alias %q", ErrInvalid, record.Binding, alias)
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
