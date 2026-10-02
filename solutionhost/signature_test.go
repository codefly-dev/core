package solutionhost_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func signedFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeSigned, name)
	require.NoError(t, err)
	return data
}

func parseSigned(t *testing.T, name string) *solutionhost.Signed {
	t.Helper()
	signed, err := solutionhost.ParseSigned(signedFixture(t, name))
	require.NoError(t, err)
	return signed
}

// A verified payload becomes a document. The name of the function is the
// contract: core verifies no signature, holds no trust root and interprets no
// bundle, so reaching here proves nothing about provenance.
func TestAVerifiedPayloadBecomesItsDocument(t *testing.T) {
	signed := parseSigned(t, "presence")
	document, err := solutionhost.PresenceFromVerified(signed.Document)
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)
	require.Equal(t, validFixtureDigest, mustDigest(t, document))

	authoritySigned := parseSigned(t, "authority")
	authorityDocument, err := solutionhost.AuthorityFromVerified(authoritySigned.Document)
	require.NoError(t, err)
	digest, err := authorityDocument.Digest()
	require.NoError(t, err)
	require.Equal(t, authorityFixtureDigest, digest)
}

func mustDigest(t *testing.T, document *solutionhost.SolutionHostBinding) string {
	t.Helper()
	digest, err := document.Digest()
	require.NoError(t, err)
	return digest
}

// The document type is inside the signed bytes, so it is attested rather than
// asserted by the carrier. A verified authority payload is refused where a
// presence document was asked for, and the other way round.
func TestAVerifiedPayloadCannotBeReadAsTheOtherType(t *testing.T) {
	presence := parseSigned(t, "presence")
	authority := parseSigned(t, "authority")

	_, err := solutionhost.AuthorityFromVerified(presence.Document)
	require.ErrorIs(t, err, solutionhost.ErrSchema)

	_, err = solutionhost.PresenceFromVerified(authority.Document)
	require.ErrorIs(t, err, solutionhost.ErrSchema)

	// The cross-type fixture is the same thing a consumer would hit: a sound
	// carrier whose payload is the other document.
	crossType := parseSigned(t, "cross-type")
	_, err = solutionhost.PresenceFromVerified(crossType.Document)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
}

// Payload bytes that are not the canonical encoding of the document they decode
// to are refused even when the attestation over them is genuine: a signer and a
// host that disagree about which bytes represent the document disagree about
// what was approved, and the digest the host stores would not match the bytes
// that were attested.
func TestNonCanonicalPayloadBytesAreRefused(t *testing.T) {
	signed := parseSigned(t, "non-canonical")

	// It parses, and it is the same document — which is what makes the check
	// worth having rather than redundant with parsing.
	parsed, err := solutionhost.Parse(signed.Document)
	require.NoError(t, err)
	require.Equal(t, validFixtureDigest, mustDigest(t, parsed))

	_, err = solutionhost.PresenceFromVerified(signed.Document)
	require.ErrorIs(t, err, solutionhost.ErrNotCanonical)
}

// The carrier refuses an unknown field, so a delivery document can never hand
// a verifier the material it is checked with. Under keyless signing the bundle
// carries a certificate, and that certificate is EVIDENCE checked against the
// caller's identity allowlist — never authority the document supplies. This is
// the check that keeps the two from blurring.
func TestACarrierCannotShipKeyMaterial(t *testing.T) {
	data := signedFixture(t, "nominates-key")
	require.Contains(t, string(data), "public_key", "the fixture is the accident this refuses")

	_, err := solutionhost.ParseSigned(data)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "public_key")

	for _, field := range []string{"certificate", "certificate_chain", "key", "jwk", "x5c", "key_url", "signature", "algorithm", "key_id"} {
		carrier := map[string]any{
			"schema":   solutionhost.SchemaSignedV1,
			"document": json.RawMessage(`{"schema":"x"}`),
			"bundle":   json.RawMessage(solutionhost.FixtureBundle),
			field:      "whatever",
		}
		encoded, err := json.Marshal(carrier)
		require.NoError(t, err)
		_, err = solutionhost.ParseSigned(encoded)
		require.ErrorIsf(t, err, solutionhost.ErrUnsigned, "field %q", field)
	}
}

// A carrier with no bundle is a document, not a signed one. Letting it through
// would make "signed" a shape rather than a claim.
func TestACarrierWithoutABundleIsNotSigned(t *testing.T) {
	_, err := solutionhost.ParseSigned(signedFixture(t, "no-bundle"))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "no signature bundle")

	for name, bundle := range map[string]string{
		"null":          `null`,
		"empty object?": ``,
	} {
		t.Run(name, func(t *testing.T) {
			carrier := `{"schema":"` + solutionhost.SchemaSignedV1 + `","document":{"schema":"x"}`
			if bundle != "" {
				carrier += `,"bundle":` + bundle
			}
			_, err := solutionhost.ParseSigned([]byte(carrier + "}"))
			require.ErrorIs(t, err, solutionhost.ErrUnsigned)
		})
	}
}

// One wire form for the bundle. A base64 string of a bundle also decodes as
// valid JSON here, and two accepted shapes is two code paths in every consumer.
func TestTheBundleMustBeAnObject(t *testing.T) {
	_, err := solutionhost.ParseSigned(signedFixture(t, "bundle-not-an-object"))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "JSON object")
}

// Core does not interpret the bundle, and that boundary is the point: a library
// that parsed a certificate out of a delivery document would be one step from
// trusting what it found there. So an arbitrary object passes, because core has
// nothing to say about it.
func TestCoreDoesNotInterpretTheBundle(t *testing.T) {
	presence := parseSigned(t, "presence")
	for _, bundle := range []string{
		`{}`,
		`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}`,
		`{"anything":{"nested":[1,2,3]}}`,
	} {
		carrier, err := solutionhost.Carrier(presence.Document, json.RawMessage(bundle))
		require.NoErrorf(t, err, "bundle %s", bundle)
		encoded, err := solutionhost.MarshalSigned(carrier)
		require.NoError(t, err)
		reparsed, err := solutionhost.ParseSigned(encoded)
		require.NoError(t, err)
		require.JSONEq(t, bundle, string(reparsed.Bundle))
	}
}

func TestParseSignedRefusesWhatIsNotACarrier(t *testing.T) {
	for name, data := range map[string][]byte{
		"not JSON at all": []byte("schema: codefly/solution-host-signed/v1\n"),
		"an empty object": []byte(`{}`),
		"no document": []byte(`{"schema":"codefly/solution-host-signed/v1","bundle":` +
			solutionhost.FixtureBundle + `}`),
		"two carriers": []byte(`{"schema":"codefly/solution-host-signed/v1","document":{"a":1},"bundle":{"b":2}} {"schema":"x"}`),
		// The three cases that a Decoder.More() check does NOT catch, which is
		// why they are here. More() is an array/object ITERATION check: it
		// answers false at a closing delimiter, so a sound carrier followed by
		// "]" or "}" satisfied it while the overall input was invalid JSON.
		// The suite stayed green when a reviewer restored More() by mutation,
		// because every existing case was a following VALUE, which More()
		// does catch. Requiring io.EOF is what refuses all of them.
		"a trailing array close":  []byte(`{"schema":"codefly/solution-host-signed/v1","document":{"a":1},"bundle":{"b":2}}]`),
		"a trailing object close": []byte(`{"schema":"codefly/solution-host-signed/v1","document":{"a":1},"bundle":{"b":2}}}`),
		"trailing garbage":        []byte(`{"schema":"codefly/solution-host-signed/v1","document":{"a":1},"bundle":{"b":2}} not json`),
		"a bare document":         []byte(`{"schema":"codefly/solution-host-binding/v2"}`),
		"another schema":          []byte(`{"schema":"codefly/solution-host-signed/v2","document":{"a":1},"bundle":{"b":2}}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.ParseSigned(data)
			require.Error(t, err)
		})
	}
}

// Core says what to sign and assembles the carrier from bytes and a bundle, and
// produces neither a signature nor a bundle. Signing is CI's, over a workload
// identity; the authority to attest a document that grants authority is not
// something a library hands to every binary that imports it.
func TestCoreSaysWhatToSignAndAssemblesTheCarrierButNeverAttests(t *testing.T) {
	document := valid(t)
	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err)
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, canonical, payload, "what was signed has one answer")

	authorityPayload, err := solutionhost.SignedPayloadFor(validAuthority(t))
	require.NoError(t, err)
	require.NotEqual(t, payload, authorityPayload)

	assembled, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	require.Equal(t, solutionhost.SchemaSignedV1, assembled.Schema)

	encoded, err := solutionhost.MarshalSigned(assembled)
	require.NoError(t, err)
	reparsed, err := solutionhost.ParseSigned(encoded)
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(reparsed.Document)
	require.NoError(t, err)
	require.Equal(t, mustDigest(t, document), mustDigest(t, verified))

	// An authority payload carries the same way.
	authorityCarrier, err := solutionhost.Carrier(authorityPayload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	_, err = solutionhost.AuthorityFromVerified(authorityCarrier.Document)
	require.NoError(t, err)
}

// Carrier refuses a payload no consumer could round-trip, so the render that
// produced it fails rather than the host that received it.
func TestCarrierRefusesWhatCannotBeRoundTripped(t *testing.T) {
	bundle := json.RawMessage(solutionhost.FixtureBundle)

	_, err := solutionhost.Carrier(nil, bundle)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)

	_, err = solutionhost.Carrier([]byte(`{"schema":"codefly/solution-host-binding/v2"}`), bundle)
	require.Error(t, err)

	// Declaration-order bytes of a real document: they parse, and they are not
	// canonical, so they are refused here rather than at the host.
	nonCanonical, err := json.Marshal(valid(t))
	require.NoError(t, err)
	_, err = solutionhost.Carrier(nonCanonical, bundle)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)

	payload, err := solutionhost.SignedPayload(valid(t))
	require.NoError(t, err)
	_, err = solutionhost.Carrier(payload, nil)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	_, err = solutionhost.Carrier(payload, json.RawMessage(`"not an object"`))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
}

// The whole signed kit, driven the way a consumer drives it: verify the bundle
// elsewhere, then hand the payload to core.
func TestSignedFixturesReachTheirOutcome(t *testing.T) {
	fixtures := solutionhost.FixturesOf(solutionhost.DocumentTypeSigned)
	require.Len(t, fixtures, 7)

	for _, shipped := range fixtures {
		t.Run(shipped.Name, func(t *testing.T) {
			require.NotEmpty(t, shipped.Reason)
			signed, err := solutionhost.ParseSigned(shipped.Document)
			if err != nil {
				require.Equal(t, solutionhost.OutcomeRejected, shipped.Outcome,
					"a carrier that does not parse never reaches a document")
				return
			}
			read := func() error {
				if shipped.Name == "authority" {
					_, err := solutionhost.AuthorityFromVerified(signed.Document)
					return err
				}
				_, err := solutionhost.PresenceFromVerified(signed.Document)
				return err
			}
			if shipped.Outcome == solutionhost.OutcomeRejected {
				require.Error(t, read())
				return
			}
			require.NoError(t, read())
		})
	}
}

// The package declares no signer, no key type and no trust store.
//
// Held by test because it is the property the whole division of labour rests
// on: a library that could attest one of these documents would put that
// authority in every binary that imports core, and signing is CI's over a
// workload identity. Checked against the package's exported DECLARATIONS —
// functions, methods, types and values — rather than its text, so the prose
// above, which has to discuss keys and trust roots to explain why they are
// absent, cannot fail it.
//
// What this guard is and is not: it is a guard against REINTRODUCTION. It
// would also pass against the commit before this one, where these surfaces
// were already absent, so it is not evidence that this PR deleted anything —
// the diff is that evidence. Its job is to make the next addition fail.
func TestThePackageDeclaresNoSigningOrTrustSurface(t *testing.T) {
	// Substrings that are unambiguous: no legitimate name in this package
	// contains one. A bare "sign" would be useless here — ErrUnsigned, Signed
	// and SignedPayload are all correct names — so the names that would denote
	// a signer are listed exactly instead, below.
	forbidden := []string{
		"signer", "signingkey", "signwith", "anchor", "trustroot", "truststore",
		"privatekey", "keypair", "publickey", "certificate", "attest", "jwks",
	}
	// Exact names that would mean core had grown the thing it must not have.
	refused := map[string]struct{}{
		"Sign": {}, "Signature": {}, "Anchor": {}, "Verifier": {}, "Key": {}, "Keys": {},
		"VerifyPresence": {}, "VerifyAuthority": {},
	}

	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, packages, "solutionhost")

	var exported []string
	for _, file := range packages["solutionhost"].Files {
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				// Methods as well as functions. A scan that collected only
				// top-level functions would pass an exported method named
				// Sign on an exported type, which is the same surface by
				// another route.
				if typed.Name.IsExported() {
					exported = append(exported, typed.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						if named.Name.IsExported() {
							exported = append(exported, named.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range named.Names {
							if name.IsExported() {
								exported = append(exported, name.Name)
							}
						}
					}
				}
			}
		}
	}
	require.NotEmpty(t, exported)

	// SignedPayload and SignedPayloadFor say what to sign; they do not sign.
	// SignedFileName and SchemaSignedV1 name the carrier. Those are the only
	// exported names allowed to contain "sign".
	allowed := map[string]struct{}{
		"SignedPayload": {}, "SignedPayloadFor": {}, "SignedFileName": {},
		"SchemaSignedV1": {}, "Signed": {}, "DocumentTypeSigned": {},
	}
	for _, name := range exported {
		require.NotContainsf(t, refused, name,
			"%s is a signing or trust surface; signing is CI's over a workload identity, and core holds no trust material", name)
		if _, fine := allowed[name]; fine {
			continue
		}
		lowered := strings.ToLower(name)
		for _, word := range forbidden {
			require.NotContainsf(t, lowered, word,
				"%s is a signing or trust surface; signing is CI's over a workload identity, and core holds no trust material", name)
		}
	}

	// And the guard guards: the names that WOULD be refused are the ones the
	// deleted ed25519 model had, so a test that passed vacuously would be a
	// test that stopped watching.
	for _, gone := range []string{"Anchor", "Signature", "VerifyPresence", "VerifyAuthority"} {
		require.NotContainsf(t, exported, gone, "%s was deleted with the key-based model", gone)
		require.Containsf(t, refused, gone, "%s must stay in the refused set", gone)
	}
}

// validCarrier is a well-formed carrier, for the trailing-input cases.
func validCarrier() []byte {
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeSigned, "presence")
	if err != nil {
		panic(err)
	}
	return data
}

// Trailing WHITESPACE is not trailing input: a JSON encoder may emit a newline
// and refusing that would refuse well-formed delivery.
func TestTrailingWhitespaceIsAccepted(t *testing.T) {
	for _, suffix := range []string{"\n", "  ", "\r\n", "\t\n "} {
		_, err := solutionhost.ParseSigned(append(validCarrier(), []byte(suffix)...))
		require.NoErrorf(t, err, "suffix %q", suffix)
	}
}
