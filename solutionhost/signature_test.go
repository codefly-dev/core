package solutionhost_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func fixtureAnchor(t *testing.T) solutionhost.Anchor {
	t.Helper()
	anchor, err := solutionhost.FixtureAnchor()
	require.NoError(t, err)
	return anchor
}

func signedFixture(t *testing.T, name string) *solutionhost.Signed {
	t.Helper()
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeSigned, name)
	require.NoError(t, err)
	signed, err := solutionhost.ParseSigned(data)
	require.NoError(t, err)
	return signed
}

// The anchor ships public keys only. There is no fixture signing key in this
// module and no signer in this package: the signatures were produced once, out
// of band, and only the bytes are committed.
func TestFixtureAnchorHoldsPublicKeysAndARevocation(t *testing.T) {
	anchor := fixtureAnchor(t)
	require.Len(t, anchor.Keys, 2)
	require.Equal(t, []string{solutionhost.FixtureRevokedKeyID}, anchor.Revoked)
	for id, key := range anchor.Keys {
		require.Lenf(t, key, ed25519.PublicKeySize, "key %q", id)
	}
	// The revoked key is still held. That is the case worth pinning: revoking
	// has to hold even while the key is present and still verifies.
	require.Contains(t, anchor.Keys, solutionhost.FixtureRevokedKeyID)
}

// A signed document verifies under the caller's anchor and yields the document.
//
// What comes back is the document as it was SIGNED, which means its
// collections are in canonical order rather than the order delivery wrote
// them. That is deliberate and it is the only honest answer: the verified
// value is the signed payload decoded, and returning the delivery order would
// mean returning something other than what the signature covers. The documents
// are the same record either way, which the digest states exactly.
func TestSignedPresenceAndAuthorityVerifyUnderTheCallersAnchor(t *testing.T) {
	anchor := fixtureAnchor(t)

	presenceDocument, err := solutionhost.VerifyPresence(signedFixture(t, "presence"), anchor)
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureBindingID, presenceDocument.Binding)
	require.Equal(t, validFixtureDigest, mustDigest(t, presenceDocument),
		"the verified document is the same record the delivered one is")

	// Canonical order, not delivery order — artifacts sorted by surface then
	// name, which is what the signature covers.
	require.Equal(t, solutionhost.SurfaceBackend, presenceDocument.Artifacts[0].Surface)

	authorityDocument, err := solutionhost.VerifyAuthority(signedFixture(t, "authority"), anchor)
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

// The document type is bound by the signature, because the schema string is
// inside the signed bytes. A genuinely signed document of one type is refused
// where the other was asked for.
func TestASignedDocumentCannotBePresentedAsTheOtherType(t *testing.T) {
	anchor := fixtureAnchor(t)

	_, err := solutionhost.VerifyAuthority(signedFixture(t, "presence"), anchor)
	require.ErrorIs(t, err, solutionhost.ErrSchema)

	_, err = solutionhost.VerifyPresence(signedFixture(t, "authority"), anchor)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
}

// A document never nominates the key it is checked with. Strict decoding
// refuses a carrier that ships one, so this never reaches a signature check at
// all — which is the point: there is nothing for a resolver to follow.
func TestACarrierCannotShipItsOwnKey(t *testing.T) {
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeSigned, "nominates-key")
	require.NoError(t, err)
	require.Contains(t, string(data), "public_key", "the fixture is the accident this refuses")

	_, err = solutionhost.ParseSigned(data)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "public_key")

	// Dropping the nominated key is the whole of what is wrong: the rest of
	// the carrier is sound and verifies. So the refusal is the field and not
	// some other defect that happens to coincide with it.
	var carried map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &carried))
	var signature map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(carried["signature"], &signature))
	delete(signature, "public_key")
	carried["signature"], err = json.Marshal(signature)
	require.NoError(t, err)
	withoutKey, err := json.Marshal(carried)
	require.NoError(t, err)

	signed, err := solutionhost.ParseSigned(withoutKey)
	require.NoError(t, err)
	document, err := solutionhost.VerifyPresence(signed, fixtureAnchor(t))
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)

	// Every shape of the same attempt, refused at the parse.
	for _, field := range []string{"certificate", "certificate_chain", "key", "jwk", "x5c", "key_url"} {
		carrier := map[string]any{
			"schema":   solutionhost.SchemaSignedV1,
			"document": json.RawMessage(`{"schema":"x"}`),
			"signature": map[string]any{
				"algorithm": solutionhost.AlgorithmEd25519,
				"key_id":    solutionhost.FixtureKeyID,
				"value":     base64.StdEncoding.EncodeToString([]byte("not-a-signature")),
				field:       "whatever",
			},
		}
		encoded, err := json.Marshal(carrier)
		require.NoError(t, err)
		_, err = solutionhost.ParseSigned(encoded)
		require.ErrorIsf(t, err, solutionhost.ErrUnsigned, "field %q", field)
	}
}

// A key id identifies a key the verifier already holds; it is never somewhere
// to fetch one. The refusal says that rather than "unknown key", which would
// send an operator looking for a key to add.
func TestAKeyIDThatSaysWhereToFetchAKeyIsRefused(t *testing.T) {
	signed := signedFixture(t, "key-id-is-a-url")
	_, err := solutionhost.VerifyPresence(signed, fixtureAnchor(t))
	require.ErrorIs(t, err, solutionhost.ErrSignature)
	require.Contains(t, err.Error(), "never where to find one")

	// The key id is the whole of what is wrong: the payload is genuinely
	// signed by the anchor's active key, so naming that key makes the same
	// bytes verify. Without this the fixture would pass whether or not its
	// signature was real, and would be pinning nothing.
	signed.Signature.KeyID = solutionhost.FixtureKeyID
	document, err := solutionhost.VerifyPresence(signed, fixtureAnchor(t))
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)

	for _, keyID := range []string{
		"https://keys.example/pub", "../../etc/keys", "key id with spaces",
		"-----BEGIN PUBLIC KEY-----", "",
	} {
		signed := signedFixture(t, "presence")
		signed.Signature.KeyID = keyID
		_, err := solutionhost.VerifyPresence(signed, fixtureAnchor(t))
		require.ErrorIsf(t, err, solutionhost.ErrSignature, "key id %q", keyID)
	}
}

// A document verifies under the caller's anchor and nothing else. The fixture
// carries a genuine signature by a key the anchor does not hold.
func TestAGenuineSignatureByAnUntrustedKeyIsRefused(t *testing.T) {
	_, err := solutionhost.VerifyPresence(signedFixture(t, "untrusted-key"), fixtureAnchor(t))
	require.ErrorIs(t, err, solutionhost.ErrSignature)
	require.Contains(t, err.Error(), "no trusted key")
}

// Revocation is checked before the key is looked up and before the signature
// is computed, so a revoked key cannot be rehabilitated by a signature that
// verifies — and the fixture's signature does verify, arithmetically.
func TestARevokedKeyIsRefusedEvenThoughItsSignatureVerifies(t *testing.T) {
	anchor := fixtureAnchor(t)
	_, err := solutionhost.VerifyPresence(signedFixture(t, "revoked-key"), anchor)
	require.ErrorIs(t, err, solutionhost.ErrSignature)
	require.Contains(t, err.Error(), "is revoked")

	// Lifting the revocation is the only thing that changes the answer, which
	// is what makes the revocation the decision and not the key's presence.
	lifted := solutionhost.Anchor{Keys: anchor.Keys}
	document, err := solutionhost.VerifyPresence(signedFixture(t, "revoked-key"), lifted)
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)
}

// Rotation is two entries in Keys for as long as both are in use, then one.
func TestRotationIsExpressibleAsTwoTrustedKeys(t *testing.T) {
	anchor := fixtureAnchor(t)

	// Only the active key, nothing revoked: the active document verifies.
	only := solutionhost.Anchor{Keys: map[string]ed25519.PublicKey{
		solutionhost.FixtureKeyID: anchor.Keys[solutionhost.FixtureKeyID],
	}}
	_, err := solutionhost.VerifyPresence(signedFixture(t, "presence"), only)
	require.NoError(t, err)

	// The outgoing key alone no longer verifies what the new one signed, which
	// is why both are held during the overlap.
	outgoing := solutionhost.Anchor{Keys: map[string]ed25519.PublicKey{
		solutionhost.FixtureRevokedKeyID: anchor.Keys[solutionhost.FixtureRevokedKeyID],
	}}
	_, err = solutionhost.VerifyPresence(signedFixture(t, "presence"), outgoing)
	require.ErrorIs(t, err, solutionhost.ErrSignature)
}

// Signature malleability, closed: a genuine signature over bytes that are not
// the canonical encoding of the document they decode to is refused. A signer
// and a host that disagree about which bytes represent the document disagree
// about what was approved — and the host's stored digest would not match the
// signed payload.
func TestNonCanonicalSignedBytesAreRefused(t *testing.T) {
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeSigned, "non-canonical")
	require.NoError(t, err)
	signed, err := solutionhost.ParseSigned(data)
	require.NoError(t, err)

	// The payload is genuinely signed and it genuinely parses: it is the same
	// document, indented.
	parsed, err := solutionhost.Parse(signed.Document)
	require.NoError(t, err)
	require.Equal(t, validFixtureDigest, mustDigest(t, parsed))

	_, err = solutionhost.VerifyPresence(signed, fixtureAnchor(t))
	require.ErrorIs(t, err, solutionhost.ErrSignature)
	require.Contains(t, err.Error(), "not the canonical encoding")
}

// A missing anchor is a verifier to fix, not a document to refuse. Verifying
// against an empty one would refuse every sound document and read as an attack.
func TestAnEmptyAnchorIsItsOwnError(t *testing.T) {
	for _, anchor := range []solutionhost.Anchor{{}, {Keys: map[string]ed25519.PublicKey{}}} {
		_, err := solutionhost.VerifyPresence(signedFixture(t, "presence"), anchor)
		require.ErrorIs(t, err, solutionhost.ErrNoAnchor)
		require.NotErrorIs(t, err, solutionhost.ErrSignature)
	}
}

// A key of the wrong length verifies nothing, and refusing is the only sound
// answer: ed25519.Verify panics on one, and the key is chosen by the untrusted
// document's key id — so one malformed anchor entry would turn every document
// naming it into a crash of the verifying process, on demand for anyone who
// learns that key id.
func TestAMalformedTrustedKeyRefusesRatherThanPanics(t *testing.T) {
	anchor := solutionhost.Anchor{Keys: map[string]ed25519.PublicKey{
		solutionhost.FixtureKeyID: []byte("too short"),
	}}
	_, err := solutionhost.VerifyPresence(signedFixture(t, "presence"), anchor)
	require.ErrorIs(t, err, solutionhost.ErrSignature)
	require.Contains(t, err.Error(), "is 9 bytes")
}

func TestVerificationRefusesEachMalformedCarrier(t *testing.T) {
	anchor := fixtureAnchor(t)
	for name, mutate := range map[string]func(*solutionhost.Signed){
		"another algorithm": func(s *solutionhost.Signed) { s.Signature.Algorithm = "rsa-pss-sha256" },
		"no algorithm":      func(s *solutionhost.Signed) { s.Signature.Algorithm = "" },
		"signature not base64": func(s *solutionhost.Signed) {
			s.Signature.Value = "not base64 !!"
		},
		"wrong signature": func(s *solutionhost.Signed) {
			s.Signature.Value = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		},
		"a flipped byte in the payload": func(s *solutionhost.Signed) {
			s.Document = []byte(strings.Replace(string(s.Document), `"generation":4`, `"generation":5`, 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			signed := signedFixture(t, "presence")
			mutate(signed)
			_, err := solutionhost.VerifyPresence(signed, anchor)
			require.ErrorIs(t, err, solutionhost.ErrSignature)
		})
	}

	_, err := solutionhost.VerifyPresence(nil, anchor)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
}

func TestParseSignedRefusesWhatIsNotACarrier(t *testing.T) {
	for name, data := range map[string][]byte{
		"not JSON at all": []byte("schema: codefly/solution-host-signed/v1\n"),
		"an empty object": []byte(`{}`),
		"no document":     []byte(`{"schema":"codefly/solution-host-signed/v1","signature":{"algorithm":"ed25519","key_id":"k","value":"AA=="}}`),
		"two carriers":    []byte(`{"schema":"codefly/solution-host-signed/v1","document":{"a":1},"signature":{"algorithm":"ed25519","key_id":"k","value":"AA=="}} {"schema":"x"}`),
		"a bare document": []byte(`{"schema":"codefly/solution-host-binding/v2"}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.ParseSigned(data)
			require.Error(t, err)
		})
	}
}

// Core ships no signer: SignedPayload says what to sign, Carrier assembles the
// delivered form from bytes and a signature, and neither takes a key. A
// library that could sign one of these documents would put the authority to
// grant authority in every binary that imports core.
func TestCoreSaysWhatToSignAndAssemblesTheCarrierButNeverSigns(t *testing.T) {
	document := valid(t)
	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err)
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, canonical, payload, "what was signed has one answer")

	authorityPayload, err := solutionhost.SignedPayloadFor(validAuthority(t))
	require.NoError(t, err)
	require.NotEqual(t, payload, authorityPayload)

	// Carrier takes a signature, never a key, and refuses one it would not
	// verify the shape of.
	signed := signedFixture(t, "presence")
	assembled, err := solutionhost.Carrier(payload, signed.Signature)
	require.NoError(t, err)
	require.Equal(t, solutionhost.SchemaSignedV1, assembled.Schema)

	encoded, err := solutionhost.MarshalSigned(assembled)
	require.NoError(t, err)
	reparsed, err := solutionhost.ParseSigned(encoded)
	require.NoError(t, err)
	verified, err := solutionhost.VerifyPresence(reparsed, fixtureAnchor(t))
	require.NoError(t, err)
	require.Equal(t, mustDigest(t, document), mustDigest(t, verified))

	for name, signature := range map[string]solutionhost.Signature{
		"another algorithm": {Algorithm: "rsa", KeyID: solutionhost.FixtureKeyID, Value: "AA=="},
		"a key id that is a URL": {
			Algorithm: solutionhost.AlgorithmEd25519, KeyID: "https://keys.example/pub", Value: "AA==",
		},
		"no signature value": {Algorithm: solutionhost.AlgorithmEd25519, KeyID: solutionhost.FixtureKeyID},
		"a value that is not base64": {
			Algorithm: solutionhost.AlgorithmEd25519, KeyID: solutionhost.FixtureKeyID, Value: "!!",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.Carrier(payload, signature)
			require.Error(t, err)
		})
	}

	_, err = solutionhost.Carrier(nil, signed.Signature)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
}

// The whole signed kit, driven the way a consumer drives it.
func TestSignedFixturesReachTheirOutcome(t *testing.T) {
	anchor := fixtureAnchor(t)
	fixtures := solutionhost.FixturesOf(solutionhost.DocumentTypeSigned)
	require.Len(t, fixtures, 7)

	for _, shipped := range fixtures {
		t.Run(shipped.Name, func(t *testing.T) {
			signed, err := solutionhost.ParseSigned(shipped.Document)
			if err != nil {
				require.Equal(t, solutionhost.OutcomeRejected, shipped.Outcome,
					"a carrier that does not parse never reaches a verification")
				return
			}
			// The caller says which type it expects; the signature binds it.
			verify := func() error {
				if shipped.Name == "authority" {
					_, err := solutionhost.VerifyAuthority(signed, anchor)
					return err
				}
				_, err := solutionhost.VerifyPresence(signed, anchor)
				return err
			}
			if shipped.Outcome == solutionhost.OutcomeRejected {
				require.Error(t, verify())
				return
			}
			require.NoError(t, verify())
		})
	}
}
