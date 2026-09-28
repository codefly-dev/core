package solutionhost

import (
	"errors"
	"fmt"
	"slices"

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

// Admit checks one desired set against this host's state and reports, per
// document and in the order given, what the host should do with it.
//
// The set is checked as a whole because two of the rules are not properties of
// a document: a route alias is unique within a host, and a generation is only
// meaningful against the one already applied. A renderer runs the same call on
// what it is about to write, so a collision is refused where it was authored
// instead of leaving the host to guess which of two claimants meant it.
//
// Admit decides admission only. It does not verify who signed the document or
// how it arrived; a host still checks provenance and its expected target before
// calling this, and applies nothing on any error.
func (host Host) Admit(documents ...*SolutionHostBinding) ([]Decision, error) {
	applied, err := host.appliedByBinding()
	if err != nil {
		return nil, err
	}
	decisions := make([]Decision, 0, len(documents))
	declared := make(map[string]struct{}, len(documents))
	var claims []composition.Claim
	for index, document := range documents {
		if err := document.Validate(); err != nil {
			return nil, fmt.Errorf("document %d: %w", index, err)
		}
		if host.Coordinate != "" && document.Host.Coordinate != host.Coordinate {
			return nil, fmt.Errorf("%w: binding %q targets %q, this host is %q", ErrWrongHost, document.Binding, document.Host.Coordinate, host.Coordinate)
		}
		// One desired set declares one generation per binding: two would make
		// the applied generation depend on the order the host read them in.
		if _, exists := declared[document.Binding]; exists {
			return nil, fmt.Errorf("%w: binding %q is declared twice in one set", ErrInvalid, document.Binding)
		}
		declared[document.Binding] = struct{}{}

		decision, err := decide(applied[document.Binding], document)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)

		for _, alias := range document.Aliases() {
			claims = append(claims, composition.Claim{Kind: composition.CollisionRoute, Key: alias, Owner: document.Binding})
		}
	}
	// An applied binding the set does not mention keeps its aliases; one the
	// set does mention releases them, because the document in hand is that
	// binding's whole desired state.
	for _, record := range host.Applied {
		if _, exists := declared[record.Binding]; exists || record.Removed {
			continue
		}
		for _, alias := range record.Routes {
			claims = append(claims, composition.Claim{Kind: composition.CollisionRoute, Key: alias, Owner: record.Binding})
		}
	}
	// Route-alias uniqueness within a host, and the host's reserved namespaces,
	// are exactly composition's collision vocabulary, so the same checker and
	// the same composition.ErrCollision answer here.
	if err := composition.ValidateCollisions(claims, host.Reserved); err != nil {
		return nil, err
	}
	return decisions, nil
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
