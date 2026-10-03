package solutionhost

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
)

// The fixtures are shipped, not test scaffolding: codefly-dev/cli renders these
// documents and the host reconciles and
// verifies them, and all three repositories need the same bytes to test
// against. Embedding them makes them reachable from another module, which a
// testdata directory alone is not.
//
// The two document directories, and not testdata itself: the README beside
// them documents the kit and is not part of it, so embedding the directory
// wholesale would ship prose a consumer walking FixtureFS has to know to skip.
//
// The signed carriers are NOT here. Every one of them is a mechanical
// transform of a document in these directories, so storing them would mean a
// change to a document silently leaving its carrier describing the old one.
// They are built on demand instead; see signedFixtures.
//
//go:embed testdata/presence testdata/authority
var fixtures embed.FS

// DocumentType is which of the three shipped shapes a fixture is: a presence
// document, an authority document, or a signature carrier around one of them.
type DocumentType string

const (
	// DocumentTypePresence is a SolutionHostBinding, read with Parse.
	DocumentTypePresence DocumentType = "presence"
	// DocumentTypeAuthority is an AuthorityDocument, read with ParseAuthority.
	DocumentTypeAuthority DocumentType = "authority"
	// DocumentTypeSigned is a signature carrier, read with ParseSigned and then
	// PresenceFromVerified or AuthorityFromVerified, once its bundle is verified.
	DocumentTypeSigned DocumentType = "signed"
)

// Outcome is what a conforming implementation must reach for a fixture.
type Outcome string

const (
	// OutcomeAccepted means the fixture is admitted, validated or verified
	// without error against the fixture state its type is checked against.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRejected means the fixture must be refused — by Parse, by
	// Validate, by Host.Admit, by ValidateAgainst, by Activate, or by whatever
	// the consumer runs in their place.
	OutcomeRejected Outcome = "rejected"
)

// Fixture is one shipped conformance document: its bytes, what must happen to
// it, and why. A consumer drives its own renderer, reconciler or verifier with
// these rather than inventing documents that agree with its own reading of the
// rules.
//
// Fixtures are driven ONE AT A TIME against the fixture state for their type:
// a presence fixture against FixtureHost, an authority fixture against
// FixtureEnvelope, a signed fixture read after its bundle is verified
// elsewhere. Several of them are
// deliberately contradictory — a tombstone and a tombstone from the wrong
// ownership domain are the same withdrawal of the same binding — so admitting
// the whole set in one call is not what any of them means.
type Fixture struct {
	// Name is the fixture's file name without its extension, unique within its
	// Type.
	Name string

	// Type is which shape this is, and therefore which parser and which
	// fixture state it is checked against.
	Type DocumentType

	// Document is the raw bytes, exactly as delivery would carry them.
	Document []byte

	// Outcome is the required result.
	Outcome Outcome

	// Reason says which rule decides the outcome.
	Reason string

	// Decision is what Host.Admit must return for an accepted presence
	// fixture, and is empty for every other type and every rejection.
	Decision Decision
}

// FixtureBindingID is the binding the accepted presence fixtures declare for a
// solution, and the one FixtureHost has already applied.
const FixtureBindingID = "alpha-region-a-01"

// FixtureModuleBindingID is the binding the accepted module presence fixture
// declares. FixtureHost has nothing applied for it, which is what makes it a
// first generation.
const FixtureModuleBindingID = "gamma-region-a-01"

// FixtureCoordinate is the host coordinate every fixture targets.
const FixtureCoordinate = "example/prod/region-a"

// FixtureDomain is the ownership domain every fixture speaks for except
// tombstone-foreign-domain, which is the point of that fixture.
const FixtureDomain = "alpha"

// FixtureEnvelopeRevision is the envelope revision every fixture was validated
// against. Both halves of an activation tuple carry it and both must agree.
const FixtureEnvelopeRevision = 7

// FixtureHost is the host state the presence fixtures are checked against: the
// "valid" fixture, applied. The relational rules need it — an older generation
// is only old against an applied one, a route alias only collides with an alias
// something else already holds, and an ownership domain is only foreign
// relative to the domain a binding was applied under.
func FixtureHost() (Host, error) {
	document, err := Parse(mustFixture(DocumentTypePresence, "valid"))
	if err != nil {
		return Host{}, err
	}
	applied, err := AppliedFrom(document)
	if err != nil {
		return Host{}, err
	}
	// The fixture host accepts the fixture domain and nothing else, which is
	// what makes tombstone-foreign-domain refusable on two counts: the host
	// does not accept that domain, and the binding was applied under another.
	return Host{
		Coordinate: FixtureCoordinate,
		Domains:    []string{FixtureDomain},
		Applied:    []Applied{applied},
	}, nil
}

// FixtureEnvelope is the ceiling the authority fixtures are checked against:
// the bindings a platform administrator wrote and the builds that have been
// approved, at one revision.
//
// It is returned by value and assembled here rather than read from a file,
// because an envelope is the one thing a document must never carry: a fixture
// envelope on disk, next to the documents it bounds, would be a ceiling
// delivered alongside the thing it is supposed to bound.
func FixtureEnvelope() Envelope {
	return Envelope{
		Revision: FixtureEnvelopeRevision,
		Bindings: []AuthorityBinding{
			{
				ID: "binding:alpha:reconcile", Revision: 3,
				Audience: "https://prod.region-a.example/operations",
				Scope:    "reconcile", Queue: "reconcile.default", Namespace: "alpha-region-a-01",
			},
			{
				ID: "binding:alpha:read", Revision: 1,
				Audience: "https://prod.region-a.example/operations",
				Scope:    "read", Queue: "read.default", Namespace: "alpha-region-a-01",
			},
			{
				ID: "binding:alpha:report", Revision: 2,
				Audience: "https://prod.region-a.example/operations",
				Scope:    "report", Queue: "report.batch", Namespace: "alpha-region-a-01",
			},
		},
		ApprovedBuilds: []ImageDigest{
			// The build the "valid" presence fixture runs.
			"sha256:3880ab5504a3f436fead6e19fb23b641443747ab55faa3f63c7b7f91b610e28f",
			// A second approved build, which no presence fixture runs. The
			// other-build authority fixture is approved for this one, so its
			// refusal is "the presence document does not name this build" and
			// never "the envelope did not approve it".
			"sha256:4d74ea6aba3e66053c0ad32dfe5dc033d7556197f39f6db4f72270b1f63d8218",
		},
	}
}

// FixtureBundle is the placeholder that stands in for a Sigstore bundle in the
// signed fixtures.
//
// It is a placeholder on purpose, and the purpose is worth stating. Core does
// not verify a bundle: signing is keyless, verification is an identity
// allowlist checked against a trust root the verifier holds, and neither
// belongs in a library every binary imports. So a real bundle in these
// fixtures would be a large opaque blob that nothing here reads, would expire
// as its certificate and transparency-log entry aged, and would invite a
// reader to believe core checks it.
//
// What the fixtures CAN pin is the boundary: that a carrier without a bundle is
// refused, that the payload must be the canonical encoding of the document it
// decodes to, that the schema binds the document type, and that a carrier
// nominating a key is refused when parsed. Those are core's half, and they are
// what these fixtures drive.
//
// A consumer testing real verification does that against sigstore-go and its
// own identity policy, with a bundle its own pipeline produced. That is not
// something core can ship for it.
const FixtureBundle = `{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","_comment":"placeholder; core verifies no bundle"}`

// Fixtures returns every shipped conformance fixture, ordered by type and then
// by name.
func Fixtures() []Fixture {
	all := []Fixture{
		{
			Name: "valid", Type: DocumentTypePresence,
			Outcome: OutcomeAccepted, Decision: DecisionCurrent,
			Reason: "a complete v2 presence document for a solution instance, and the generation FixtureHost has already applied",
		},
		{
			Name: "module-presence", Type: DocumentTypePresence,
			Outcome: OutcomeAccepted, Decision: DecisionApply,
			Reason: "the same shape for a module instance: presence covers both kinds, and the kind is declared rather than inferred",
		},
		{
			Name: "tombstone", Type: DocumentTypePresence,
			Outcome: OutcomeAccepted, Decision: DecisionApply,
			Reason: "removal is a generation: the binding is declared absent at a higher generation, under the domain it was applied with",
		},
		{
			Name: "tombstone-foreign-domain", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "a withdrawal from a delivery that speaks for another ownership domain; the generation is newer, and ownership is what refuses it",
		},
		{
			Name: "stale-generation", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "the document declares a generation older than the applied one; it is rejected, not merged",
		},
		{
			Name: "mixed-release", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "one generation names artifacts rendered from two releases; a partial rollout is not declarable",
		},
		{
			Name: "duplicate-route-alias", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "a second binding claims the route alias " + FixtureBindingID + " already holds; " +
				"route aliases are unique within a host and the collision is refused before the generation applies",
		},
		{
			Name: "wrong-kind", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "the kind is neither solution nor module; admission refuses a presence entry whose kind is absent or unknown rather than guessing",
		},
		{
			Name: "missing-identity", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "the workload names no SPIFFE ID, so there is nothing a host could verify the destination's SVID against",
		},
		{
			Name: "digest-confusion", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "an image digest and a rendered digest are the same string; two digests of different kinds are never equal by chance",
		},
		{
			Name: "superseded-schema", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "a v1 presence document; there is no v1 reader, and the refusal is a version skew rather than an invalid document",
		},
		{
			Name: "valid", Type: DocumentTypeAuthority, Outcome: OutcomeAccepted,
			Reason: "a complete authority document, inside FixtureEnvelope, approved for the build the valid presence fixture runs",
		},
		{
			Name: "tombstone", Type: DocumentTypeAuthority, Outcome: OutcomeAccepted,
			Reason: "withdrawal of authority is a generation; it is a sound document that activates nothing",
		},
		{
			Name: "outside-envelope", Type: DocumentTypeAuthority, Outcome: OutcomeRejected,
			Reason: "it grants a binding FixtureEnvelope does not hold; containment is exact element inclusion, never subsumption",
		},
		{
			Name: "other-build", Type: DocumentTypeAuthority, Outcome: OutcomeRejected,
			Reason: "it is approved for a build the valid presence fixture does not name, so the tuple does not activate",
		},
		{
			Name: "presence", Type: DocumentTypeSigned, Outcome: OutcomeAccepted,
			Reason: "the valid presence document as its canonical bytes, carried with a bundle; PresenceFromVerified accepts it",
		},
		{
			Name: "authority", Type: DocumentTypeSigned, Outcome: OutcomeAccepted,
			Reason: "the valid authority document as its canonical bytes, carried with a bundle",
		},
		{
			Name: "nominates-key", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "the carrier ships a public key beside its bundle; strict decoding refuses it, so a delivery document " +
				"can never hand a verifier the material it is checked with",
		},
		{
			Name: "no-bundle", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "a carrier with no signature bundle is a document, not a signed one; letting it through would make " +
				"\"signed\" a shape rather than a claim",
		},
		{
			Name: "bundle-not-an-object", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "the bundle is a base64 string rather than the object a bundle is; one wire form, because two " +
				"accepted shapes is two code paths in every consumer",
		},
		{
			Name: "cross-type", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "an authority payload presented where a presence document was asked for; the schema is inside the " +
				"signed bytes, so the document type is attested rather than asserted by the carrier",
		},
		{
			Name: "non-canonical", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "bytes that are not the canonical encoding of the document they decode to; refused even when the " +
				"attestation over them is genuine, because a signer and a host that disagree about which bytes are " +
				"the document disagree about what was approved",
		},
	}
	for index := range all {
		all[index].Document = mustFixture(all[index].Type, all[index].Name)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Type != all[j].Type {
			return all[i].Type < all[j].Type
		}
		return all[i].Name < all[j].Name
	})
	return all
}

// FixturesOf returns the shipped fixtures of one type, ordered by name.
func FixturesOf(documentType DocumentType) []Fixture {
	var of []Fixture
	for _, fixture := range Fixtures() {
		if fixture.Type == documentType {
			of = append(of, fixture)
		}
	}
	return of
}

// documentTypes are the shipped types, which is also the set of directories
// under testdata that hold documents.
var documentTypes = []DocumentType{DocumentTypeAuthority, DocumentTypePresence, DocumentTypeSigned}

// fixtureFile is where one fixture lives under testdata. The extension follows
// the shape: a presence or authority document is YAML as delivery carries it,
// and a signature carrier is JSON for the reason SignedFileName gives.
func fixtureFile(documentType DocumentType, name string) (string, error) {
	if !slices.Contains(documentTypes, documentType) {
		return "", fmt.Errorf("solution host fixture: %q is not one of %v", documentType, documentTypes)
	}
	return path.Join("testdata", string(documentType), name+".codefly.yaml"), nil
}

// signedCarrier assembles one signed-fixture carrier from a payload and a
// bundle, without going through Carrier — several of these are carriers the
// library refuses to build, which is exactly what makes them worth shipping.
func signedCarrier(payload, bundle string, extra string) []byte {
	carrier := `{"schema":"` + SchemaSignedV1 + `","document":` + payload
	if bundle != "" {
		carrier += `,"bundle":` + bundle
	}
	if extra != "" {
		carrier += "," + extra
	}
	return []byte(carrier + "}")
}

// signedFixtures are built from the presence and authority documents rather
// than committed as bytes, because every one of them is a mechanical transform
// of those documents and committing them would mean a change to a document
// silently leaving its carrier describing the old one.
func signedFixtures(name string) ([]byte, error) {
	presence, err := Parse(mustFixture(DocumentTypePresence, "valid"))
	if err != nil {
		return nil, err
	}
	presencePayload, err := SignedPayload(presence)
	if err != nil {
		return nil, err
	}
	authority, err := ParseAuthority(mustFixture(DocumentTypeAuthority, "valid"))
	if err != nil {
		return nil, err
	}
	authorityPayload, err := SignedPayloadFor(authority)
	if err != nil {
		return nil, err
	}
	switch name {
	case "presence":
		return signedCarrier(string(presencePayload), FixtureBundle, ""), nil
	case "authority":
		return signedCarrier(string(authorityPayload), FixtureBundle, ""), nil
	case "cross-type":
		// An authority payload, carried where a presence document is asked
		// for. The carrier itself is sound; the schema inside the signed bytes
		// is what refuses it.
		return signedCarrier(string(authorityPayload), FixtureBundle, ""), nil
	case "nominates-key":
		return signedCarrier(string(presencePayload), FixtureBundle,
			`"public_key":"MCowBQYDK2VwAyEA"`), nil
	case "no-bundle":
		return signedCarrier(string(presencePayload), "", ""), nil
	case "bundle-not-an-object":
		return signedCarrier(string(presencePayload), `"eyJtZWRpYVR5cGUiOiJ4In0="`, ""), nil
	case "non-canonical":
		// Declaration-order keys: what a signer that reached for encoding/json
		// instead of CanonicalBytes produces. It parses to the same document
		// and is not its canonical encoding.
		declarationOrder, err := json.Marshal(presence)
		if err != nil {
			return nil, err
		}
		return signedCarrier(string(declarationOrder), FixtureBundle, ""), nil
	}
	return nil, fmt.Errorf("solution host signed fixture %q: no such fixture", name)
}

// FixtureDocument returns one fixture's raw bytes by type and name.
func FixtureDocument(documentType DocumentType, name string) ([]byte, error) {
	// A signed carrier is derived from the documents it carries, not stored.
	if documentType == DocumentTypeSigned {
		return signedFixtures(name)
	}
	file, err := fixtureFile(documentType, name)
	if err != nil {
		return nil, err
	}
	data, err := fixtures.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("solution host %s fixture %q: %w", documentType, name, err)
	}
	return data, nil
}

// FixtureFS returns the document fixtures as a filesystem rooted at testdata,
// for a consumer that would rather walk them than name them. Documents live
// under presence/ and authority/.
//
// The signed carriers are NOT here. Each is a mechanical transform of one of
// these documents, so storing it would mean a change to a document silently
// leaving its carrier describing the old one; FixtureDocument builds them on
// demand instead.
func FixtureFS() fs.FS {
	sub, err := fs.Sub(fixtures, "testdata")
	if err != nil {
		panic(err)
	}
	return sub
}

func mustFixture(documentType DocumentType, name string) []byte {
	data, err := FixtureDocument(documentType, name)
	if err != nil {
		// The fixtures are embedded at build time, so a missing one is a broken
		// build of this package rather than a runtime condition a caller could
		// handle.
		panic(err)
	}
	return data
}
