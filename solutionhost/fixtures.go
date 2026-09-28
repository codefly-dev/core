package solutionhost

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// The fixtures are shipped, not test scaffolding: codefly-dev/cli renders this
// document and the host in codefly-dev/module-saas-starter reconciles it, and
// both need the same bytes to test against. Embedding them makes them reachable
// from another module, which a testdata directory alone is not.
//
//go:embed testdata/*.codefly.yaml
var fixtures embed.FS

// Outcome is what a conforming implementation must reach for a fixture, checked
// against FixtureHost.
type Outcome string

const (
	// OutcomeAccepted means Host.Admit returns a decision and no error.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRejected means the fixture must be refused — by Validate, by
	// Host.Admit, or by whatever the consumer runs in their place.
	OutcomeRejected Outcome = "rejected"
)

// Fixture is one shipped conformance document: its bytes, what must happen to
// it, and why. A consumer drives its own renderer or reconciler with these
// rather than inventing documents that agree with its own reading of the spec.
type Fixture struct {
	// Name is the fixture's file name without its extension.
	Name string
	// Document is the raw YAML, exactly as delivery would carry it.
	Document []byte
	// Outcome is the required result against FixtureHost.
	Outcome Outcome
	// Reason says which rule decides the outcome.
	Reason string
	// Decision is what Host.Admit must return for an accepted fixture.
	Decision Decision
}

// FixtureBindingID is the binding the accepted fixtures declare, and the one
// FixtureHost has already applied.
const FixtureBindingID = "crm-eu-west-1-01"

// FixtureCoordinate is the host coordinate every fixture targets.
const FixtureCoordinate = "obin/prod/eu-west-1"

// FixtureHost is the host state the fixtures are checked against: the "valid"
// fixture, applied. The relational rules need it — an older generation is only
// old against an applied one, and a route alias only collides with an alias
// something else already holds.
func FixtureHost() (Host, error) {
	document, err := Parse(mustFixture("valid"))
	if err != nil {
		return Host{}, err
	}
	applied, err := AppliedFrom(document)
	if err != nil {
		return Host{}, err
	}
	return Host{Coordinate: FixtureCoordinate, Applied: []Applied{applied}}, nil
}

// Fixtures returns every shipped conformance fixture, ordered by name.
func Fixtures() []Fixture {
	all := []Fixture{
		{
			Name: "duplicate-route-alias", Outcome: OutcomeRejected,
			Reason: "a second binding claims the route alias crm-eu-west-1-01 already holds; " +
				"route aliases are unique within a host and the collision is refused before the generation applies",
		},
		{
			Name: "mixed-release", Outcome: OutcomeRejected,
			Reason: "one generation names artifacts rendered from two releases; a partial rollout is not declarable",
		},
		{
			Name: "stale-generation", Outcome: OutcomeRejected,
			Reason: "the document declares a generation older than the applied one; it is rejected, not merged",
		},
		{
			Name: "tombstone", Outcome: OutcomeAccepted, Decision: DecisionApply,
			Reason: "removal is a generation: the binding is declared absent at a higher generation than the applied one",
		},
		{
			Name: "valid", Outcome: OutcomeAccepted, Decision: DecisionCurrent,
			Reason: "a complete v1 document, and the generation FixtureHost has already applied",
		},
	}
	for index := range all {
		all[index].Document = mustFixture(all[index].Name)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// FixtureDocument returns one fixture's raw bytes by name.
func FixtureDocument(name string) ([]byte, error) {
	data, err := fixtures.ReadFile(path.Join("testdata", name+".codefly.yaml"))
	if err != nil {
		return nil, fmt.Errorf("solution host binding fixture %q: %w", name, err)
	}
	return data, nil
}

// FixtureFS returns the fixtures as a filesystem rooted at the documents, for a
// consumer that would rather walk them than name them.
func FixtureFS() fs.FS {
	sub, err := fs.Sub(fixtures, "testdata")
	if err != nil {
		panic(err)
	}
	return sub
}

func mustFixture(name string) []byte {
	data, err := FixtureDocument(name)
	if err != nil {
		// The fixtures are embedded at build time, so a missing one is a broken
		// build of this package rather than a runtime condition a caller could
		// handle.
		panic(err)
	}
	return data
}
