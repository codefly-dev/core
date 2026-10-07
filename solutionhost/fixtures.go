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

	// Message is text the refusal of a rejected fixture must carry, whichever
	// entrypoint refused it — Parse, Host.Admit, ValidateAgainst, Activate or
	// verification — so a consumer asserts the reason and not only the
	// outcome. Empty for an accepted fixture.
	Message string

	// Rule names the table rule a rejected fixture protects (the decoding
	// rules every field shares and the build-size rules; see BuildSize): the
	// one a reader cannot drop without this fixture noticing, which the
	// package's self-check proves by deleting each rule in turn. Empty for an
	// accepted fixture and for a refusal the table does not hold.
	Rule string
}

// FixtureBindingID is the binding the accepted presence fixtures declare for a
// solution, and the one FixtureHost has already applied.
const FixtureBindingID = "alpha-region-a-01"

// fixtureOperationsAudience is the audience the fixture bindings are issued
// for, once, so the fixtures cannot drift apart on it.
const fixtureOperationsAudience = "https://prod.region-a.example/operations"

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
		// And which signer may speak for it. A document asserts its own
		// domain, so the host must say who is allowed to make that assertion;
		// see Host.DomainsBySigner.
		DomainsBySigner: map[string][]string{FixtureDeliveredBy: {FixtureDomain}},
		Applied:         []Applied{applied},
	}, nil
}

// FixtureEnvelope is the ceiling the authority fixtures are checked against:
// who may hold which binding, as a platform administrator wrote it, and the
// builds that have been approved, at one revision.
//
// It is returned by value and assembled here rather than read from a file,
// because an envelope is the one thing a document must never carry: a fixture
// envelope on disk, next to the documents it bounds, would be a ceiling
// delivered alongside the thing it is supposed to bound.
func FixtureEnvelope() Envelope {
	return Envelope{
		Revision: FixtureEnvelopeRevision,
		// Each grant names WHO may hold the binding, matching the "valid"
		// authority fixture: the operator holds reconcile and read, the
		// reporter holds report. A ceiling that listed only the bindings let a
		// document move reconcile to any principal and still pass.
		Grants: []EnvelopeGrant{
			{
				Principal: "principal:operator",
				Binding: AuthorityBinding{
					ID: "binding:alpha:reconcile", Revision: 3,
					Audience: fixtureOperationsAudience,
					Scope:    "reconcile", Queue: "reconcile.default", Namespace: FixtureBindingID,
				},
			},
			{
				Principal: "principal:operator",
				Binding: AuthorityBinding{
					ID: "binding:alpha:read", Revision: 1,
					Audience: fixtureOperationsAudience,
					Scope:    "read", Queue: "read.default", Namespace: FixtureBindingID,
				},
			},
			{
				Principal: "principal:reporter",
				Binding: AuthorityBinding{
					ID: "binding:alpha:report", Revision: 2,
					Audience: fixtureOperationsAudience,
					Scope:    "report", Queue: "report.batch", Namespace: FixtureBindingID,
				},
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

// FixtureDeliveredBy is the delivering identity a consumer's BundleVerifier is
// expected to name for the shipped fixtures — a keyless certificate SAN in the shape
// a workflow identity takes. Core verifies no attestation, so this is what the
// fixture host's signer policy is written against rather than anything core
// checks.
const FixtureDeliveredBy = "https://signer.example/workflow@refs/heads/main"

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
			Reason:  "a withdrawal from a delivery that speaks for another ownership domain; the generation is newer, and ownership is what refuses it",
			Message: `which host "example/prod/region-a" does not accept`,
		},
		{
			Name: "stale-generation", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason:  "the document declares a generation older than the applied one; it is rejected, not merged",
			Message: "declares generation 3, applied is 4",
		},
		{
			Name: "mixed-release", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason:  "one generation names artifacts rendered from two releases; a partial rollout is not declarable",
			Message: `was rendered from "example/beta@1.9.0", not "example/beta@2.0.0"`,
		},
		{
			Name: "duplicate-route-alias", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "a second binding claims the route alias " + FixtureBindingID + " already holds; " +
				"route aliases are unique within a host and the collision is refused before the generation applies",
			Message: `route "alpha" is claimed by both`,
		},
		{
			Name: "wrong-kind", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason:  "the kind is neither solution nor module; admission refuses a presence entry whose kind is absent or unknown rather than guessing",
			Message: `kind "service" is not one of`,
		},
		{
			Name: "missing-identity", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason:  "the workload names no SPIFFE ID, so there is nothing a host could verify the destination's SVID against",
			Message: "requires a SPIFFE ID for the SVID it must present",
		},
		{
			Name: "digest-confusion", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason:  "an image digest and a rendered digest are the same string; two digests of different kinds are never equal by chance",
			Message: "are the same digest",
		},
		{
			Name: "endpoint-exposure-omitted", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "a public endpoint that states no exposure; reach is not addressing, so a platform standing up a route " +
				"has no field left to read and a reader that took the reach for the answer would conflate the two again",
			Message: `declares visibility "public" and states no exposure`,
		},
		{
			Name: "endpoint-exposure-beyond-reach", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "an outward address allocated for an endpoint nothing outside the workspace may call; the two axes are " +
				"read on their own, which is not the same as being independent",
			Message: `states exposure "public" with visibility "internal"`,
		},
		{
			Name: "endpoint-exposure-unknown", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Reason: "an exposure the resource model does not define; addressing has one vocabulary, and a route with hostnames " +
				"is the deployment's ingress rather than a third spelling here",
			Message: `exposure "ingress" is neither "public" nor "none"`,
		},
		{
			Name: "superseded-schema", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule:    ruleSchema,
			Reason:  "a v1 presence document; there is no v1 reader, and the refusal is a version skew rather than an invalid document",
			Message: `"codefly/solution-host-binding/v1" (this Core reads`,
		},
		{
			Name: "superseded-schema-v2", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleSchema,
			Reason: "a complete v2 presence document: sound on its own terms, and unable to say whether an address was " +
				"allocated for its public endpoint, because v2 had no field for it. v2 is the shape delivery " +
				"repositories hold, so the cutover is a refusal to pin rather than discover",
			Message: `"codefly/solution-host-binding/v2" (this Core reads`,
		},
		{
			Name: "build-size", Type: DocumentTypePresence,
			Outcome: OutcomeAccepted, Decision: DecisionApply,
			Reason: "the valid document at the next generation, carrying the build's size: lines per language, backend and frontend, " +
				"the totals, and the vendored paths the manifest declared and the producer excluded; the section is optional in v2 " +
				"and held to the build-size rules when present",
		},
		{
			Name: "build-size-languages-omitted", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeLanguagesDeclared, Message: "build_size.languages must be declared",
			Reason: "the section declares no languages list; an absent list and \"no file in a known language was counted\" must not look the same",
		},
		{
			Name: "build-size-unknown-language", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeLanguageKnown, Message: `build_size language "cobol" is not one of`,
			Reason: "a row names a language this Core does not know; the set is closed and read from core, never guessed at",
		},
		{
			Name: "build-size-language-twice", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeLanguageUnique, Message: `build_size language "go" is declared twice`,
			Reason: "one language is one row carrying both its backend and its frontend lines; two rows make the figure depend on which a reader took",
		},
		{
			Name: "build-size-empty-language", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeLanguageCounts, Message: `build_size language "rust" counts no line`,
			Reason: "a row counts no line on either side; a language that was not found is not written",
		},
		{
			Name: "build-size-vendored-omitted", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeVendoredDeclared, Message: "build_size.vendored must be declared",
			Reason: "the section declares no vendored list; \"nothing was excluded\" is a fact the manifest stated and the document repeats as an empty list",
		},
		{
			Name: "build-size-vendored-trailing-slash", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeVendoredCanonical, Message: `build_size vendored path "web/src/clients/" is not a canonical relative path`,
			Reason: "an excluded path spelled with a trailing slash; one prefix has one spelling, path.Clean's, in the manifest and in the document that repeats it",
		},
		{
			Name: "build-size-vendored-twice", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeVendoredUnique, Message: `build_size vendored path "web/src/clients" is declared twice`,
			Reason: "the same excluded path declared twice",
		},
		{
			Name: "build-size-vendored-nested", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeVendoredDisjoint, Message: `build_size vendored paths "web/src/clients" and "web/src/clients/go" overlap`,
			Reason: "one excluded path lies under another; a path inside an excluded one excludes nothing more, so the outer one is declared alone",
		},
		{
			Name: "build-size-backend-total-disagrees", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeBackendTotal, Message: "build_size.backend is 12000 and its languages' backend lines sum to 12416",
			Reason: "the backend total is not the sum of the rows' backend lines; a document whose totals do not equal the sum of its parts states two sizes",
		},
		{
			Name: "build-size-frontend-total-disagrees", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeFrontendTotal, Message: "build_size.frontend is 8000 and its languages' frontend lines sum to 8102",
			Reason: "the frontend total is not the sum of the rows' frontend lines",
		},
		{
			Name: "build-size-total-disagrees", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeTotal, Message: "build_size.total is 20000 and backend plus frontend is 20518",
			Reason: "the overall total is not backend plus frontend while both of those agree with their rows",
		},
		{
			Name: "tombstone-with-build-size", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule:    ruleBuildSizeAbsentWhenRemoved,
			Message: "a removed generation declares no build_size",
			Reason:  "a tombstone carrying a build size; a removed generation declares no build, so it declares no size of one",
		},
		{
			Name: "not-yaml", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleWellFormed, Message: "not a YAML document",
			Reason: "not YAML at all; syntax is a precondition of reading anything, listed with the rules so the kit covers it",
		},
		{
			Name: "two-documents", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleOneDocument, Message: "holds more than one document",
			Reason: "a second YAML document in the file; a reader that took the first and ignored the rest would sign something other than the file",
		},
		{
			Name: "build-size-unknown-field", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleKnownFields, Message: "field vendorred not found",
			Reason: "the exclusion list spelled vendorred; an unknown field is refused, never dropped, or the count would include the kit while the document said nothing was excluded",
		},
		{
			Name: "build-size-field-twice", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleMappingKeysOnce, Message: `names the key "backend" twice`,
			Reason: "backend named twice in the section; a repeated key makes the decoded document depend on which the reader kept",
		},
		{
			Name: "build-size-count-null", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleNoNulls, Message: "explicit null at build_size.total",
			Reason: "the total written as null; yaml decodes a null count as zero and reports nothing",
		},
		{
			Name: "build-size-count-fractional", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleWholeNumbers, Message: "is not a whole number",
			Reason: "a row's backend lines written as 12416.9; yaml truncates the fraction before any rule sees it, and the totals then agree with a row the file never wrote",
		},
		{
			Name: "build-size-empty-key", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleKeyIsAName, Message: "a key that is not a name",
			Reason: "a nameless key in the section; its mappings are keyed by name",
		},
		{
			Name: "build-size-total-omitted", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeFieldsDeclared, Message: "build_size.total must be declared",
			Reason: "no total; an absent count decodes as zero, and a zero nobody wrote is a total that agrees with nothing",
		},
		{
			Name: "build-size-row-frontend-omitted", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeFieldsDeclared, Message: "build_size.languages[0].frontend must be declared",
			Reason: "a row without its frontend count; the missing count would decode as zero and every total would agree",
		},
		{
			Name: "build-size-count-hex", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeCountsDecimal, Message: `build_size.backend is "0x3080", not a whole number written in decimal digits`,
			Reason: "a total written in hexadecimal; a count has one spelling, decimal digits, as this package writes it",
		},
		{
			Name: "build-size-count-leading-zero", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeCountsDecimal, Message: `build_size.languages[0].backend is "012416", not a whole number written in decimal digits`,
			Reason: "a row's count with a leading zero; yaml reads it as octal, so the document would be validated on a number nobody wrote and accepted",
		},
		{
			Name: "build-size-count-negative", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeCountsDecimal, Message: `build_size.frontend is "-8102", not a whole number written in decimal digits`,
			Reason: "a total written negative; a count is unsigned, and the refusal is the node rule's, by name, rather than whatever the typed decoder says about a sign",
		},
		{
			Name: "build-size-count-too-large", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeCountsFit, Message: "is 18446744073709551616, more than a count can hold",
			Reason: "a row's count tagged an integer and one past uint64; a plain one yaml tags a float and the whole-number rule refuses, so this is the explicit spelling that reaches the count",
		},
		{
			Name: "build-size-backend-sum-overflows", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeBackendSumFits, Message: "backend lines sum to more than a count can hold",
			Reason: "two rows whose backend lines sum past uint64, with the wrapped sum as the stated total; the sum fitting is refused before the comparison that would agree by accident",
		},
		{
			Name: "build-size-frontend-sum-overflows", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeFrontendSumFits, Message: "frontend lines sum to more than a count can hold",
			Reason: "two rows whose frontend lines sum past uint64, with the wrapped sum as the stated total",
		},
		{
			Name: "build-size-total-overflows", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeTotalFits, Message: "backend plus frontend is more than a count can hold",
			Reason: "side totals that agree with their rows but sum past uint64, with the wrapped sum as the stated total",
		},
		{
			Name: "build-size-vendored-not-utf8", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleBuildSizeVendoredCanonical, Message: `vendored path "vendor/\xff" is not a canonical relative path`,
			Reason: "an excluded path written as bytes that are not UTF-8; the signing encoding would rewrite it, so the signed document would name a path the counter never excluded",
		},
		{
			Name: "build-size-merge-key", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleNoMergeKeys, Message: "the mapping at build_size uses the merge key",
			Reason: "the section's languages arrive through a merge key; yaml applies a merge after every node rule has run, so the octal count it carries would be repaired unseen",
		},
		{
			Name: "merge-key-at-top-level", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleNoMergeKeys, Message: "the mapping at the document's top level uses the merge key",
			Reason: "the whole section arrives through a top-level merge key, so no node rule finds it; a signed count and an omitted count would be repaired unseen",
		},
		{
			Name: "merge-key", Type: DocumentTypeAuthority, Outcome: OutcomeRejected,
			Message: "uses the merge key",
			Reason:  "the authority reader shares the presence reader's node checks and refuses a merge key the same way, rather than implementing the refusal twice",
		},
		{
			Name: "build-size-binary-key", Type: DocumentTypePresence, Outcome: OutcomeRejected,
			Rule: ruleKeyIsAName, Message: `a key that is not a name ("!!binary`,
			Reason: "the section's key spelled !!binary; the typed decoder resolves it to build_size while a rule looking for the key by its written text never sees it, so the octal count it hides would be repaired and signed",
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
			Reason:  "it grants a binding FixtureEnvelope does not hold; containment is exact element inclusion, never subsumption",
			Message: "binding:alpha:administer",
		},
		{
			Name: "other-build", Type: DocumentTypeAuthority, Outcome: OutcomeRejected,
			Reason:  "it is approved for a build the valid presence fixture does not name, so the tuple does not activate",
			Message: "is approved for build",
		},
		{
			Name: string(DocumentTypePresence), Type: DocumentTypeSigned, Outcome: OutcomeAccepted,
			Reason: "the valid presence document as its canonical bytes, carried with a bundle; PresenceFromVerified accepts it",
		},
		{
			Name: string(DocumentTypeAuthority), Type: DocumentTypeSigned, Outcome: OutcomeAccepted,
			Reason: "the valid authority document as its canonical bytes, carried with a bundle",
		},
		{
			Name: "nominates-key", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "the carrier ships a public key beside its bundle; strict decoding refuses it, so a delivery document " +
				"can never hand a verifier the material it is checked with",
			Message: `unknown field "public_key"`,
		},
		{
			Name: "no-bundle", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "a carrier with no signature bundle is a document, not a signed one; letting it through would make " +
				"\"signed\" a shape rather than a claim",
			Message: "the carrier holds no signature bundle",
		},
		{
			Name: "bundle-not-an-object", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "the bundle is a base64 string rather than the object a bundle is; one wire form, because two " +
				"accepted shapes is two code paths in every consumer",
			Message: "the signature bundle must be a JSON object",
		},
		{
			Name: "cross-type", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "an authority payload presented where a presence document was asked for; the schema is inside the " +
				"signed bytes, so the document type is attested rather than asserted by the carrier",
			Message: `"codefly/solution-authority/v1" (this Core reads`,
		},
		{
			Name: "non-canonical", Type: DocumentTypeSigned, Outcome: OutcomeRejected,
			Reason: "bytes that are not the canonical encoding of the document they decode to; refused even when the " +
				"attestation over them is genuine, because a signer and a host that disagree about which bytes are " +
				"the document disagree about what was approved",
			Message: "not its canonical encoding",
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
