package solutionhost_test

import (
	"context"
	"encoding/json"
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

// These decoding helpers alone prove nothing about provenance. Admission
// requires VerifyDelivered, which verifies the envelope before parsing.
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

// The carrier cannot nominate its own trust material beside the envelope.
func TestACarrierCannotShipKeyMaterial(t *testing.T) {
	data := signedFixture(t, "nominates-key")
	require.Contains(t, string(data), "public_key", "the fixture is the accident this refuses")

	_, err := solutionhost.ParseSigned(data)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "public_key")

	for _, field := range []string{"certificate", "certificate_chain", "key", "jwk", "x5c", "key_url", "signature", "algorithm", "key_id"} {
		carrier := map[string]any{
			"document": json.RawMessage(`{"schema":"x"}`),
			"envelope": parseSigned(t, "presence").Envelope,
			field:      "whatever",
		}
		encoded, err := json.Marshal(carrier)
		require.NoError(t, err)
		_, err = solutionhost.ParseSigned(encoded)
		require.ErrorIsf(t, err, solutionhost.ErrUnsigned, "field %q", field)
	}
}

// A carrier with no envelope is a document, not a signed one. Letting it through
// would make "signed" a shape rather than a claim.
func TestACarrierWithoutAEnvelopeIsNotSigned(t *testing.T) {
	_, err := solutionhost.ParseSigned(signedFixture(t, "no-envelope"))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "no signature envelope")

	for name, envelope := range map[string]string{
		"null":          `null`,
		"empty object?": ``,
	} {
		t.Run(name, func(t *testing.T) {
			carrier := `{"document":{"schema":"x"}`
			if envelope != "" {
				carrier += `,"envelope":` + envelope
			}
			_, err := solutionhost.ParseSigned([]byte(carrier + "}"))
			require.ErrorIs(t, err, solutionhost.ErrUnsigned)
		})
	}
}

// One wire form for the envelope. A base64 string of an envelope also decodes as
// valid JSON here, and two accepted shapes is two code paths in every consumer.
func TestTheEnvelopeMustBeAnObject(t *testing.T) {
	_, err := solutionhost.ParseSigned(signedFixture(t, "envelope-not-an-object"))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "JSON object")
}

// Shape parsing preserves envelope bytes; acceptance requires verification.
func TestCarrierParsingDoesNotVerifyTheEnvelope(t *testing.T) {
	presence := parseSigned(t, "presence")
	for _, envelope := range []string{
		`{}`,
		`{"payloadType":"application/json"}`,
		`{"anything":{"nested":[1,2,3]}}`,
	} {
		carrier, err := solutionhost.Carrier(presence.Document, json.RawMessage(envelope))
		require.NoErrorf(t, err, "envelope %s", envelope)
		encoded, err := solutionhost.MarshalSigned(carrier)
		require.NoError(t, err)
		reparsed, err := solutionhost.ParseSigned(encoded)
		require.NoError(t, err)
		require.JSONEq(t, envelope, string(reparsed.Envelope))
		_, err = solutionhost.VerifyDelivered(context.Background(), reparsed, fixtureVerifier(t))
		require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	}
}

func TestParseSignedRefusesWhatIsNotACarrier(t *testing.T) {
	for name, data := range map[string][]byte{
		"not JSON at all": []byte("schema: codefly/solution-host-signed/v1\n"),
		"an empty object": []byte(`{}`),
		"no document": []byte(`{"envelope":` +
			string(parseSigned(t, "presence").Envelope) + `}`),
		"two carriers": []byte(`{"document":{"a":1},"envelope":{"b":2}} {"schema":"x"}`),
		// The three cases that a Decoder.More() check does NOT catch, which is
		// why they are here. More() is an array/object ITERATION check: it
		// answers false at a closing delimiter, so a sound carrier followed by
		// "]" or "}" satisfied it while the overall input was invalid JSON.
		// The suite stayed green when a reviewer restored More() by mutation,
		// because every existing case was a following VALUE, which More()
		// does catch. Requiring io.EOF is what refuses all of them.
		"a trailing array close":  []byte(`{"document":{"a":1},"envelope":{"b":2}}]`),
		"a trailing object close": []byte(`{"document":{"a":1},"envelope":{"b":2}}}`),
		"trailing garbage":        []byte(`{"document":{"a":1},"envelope":{"b":2}} not json`),
		"a bare document":         []byte(`{"schema":"codefly/solution-host-binding/v1"}`),
		"another schema":          []byte(`{"schema":"codefly/solution-host-signed/v2","document":{"a":1},"envelope":{"b":2}}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.ParseSigned(data)
			require.Error(t, err)
		})
	}
}

// Canonical payload helpers and Carrier preserve the exact signed bytes.
func TestCanonicalPayloadAndCarrierRoundTrip(t *testing.T) {
	document := valid(t)
	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err)
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, canonical, payload, "what was signed has one answer")

	authorityPayload, err := solutionhost.SignedPayloadFor(validAuthority(t))
	require.NoError(t, err)
	require.NotEqual(t, payload, authorityPayload)

	assembled, err := solutionhost.Carrier(payload, fixtureEnvelope(t, payload))
	require.NoError(t, err)

	encoded, err := solutionhost.MarshalSigned(assembled)
	require.NoError(t, err)
	reparsed, err := solutionhost.ParseSigned(encoded)
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(reparsed.Document)
	require.NoError(t, err)
	require.Equal(t, mustDigest(t, document), mustDigest(t, verified))

	// An authority payload carries the same way.
	authorityCarrier, err := solutionhost.Carrier(authorityPayload, fixtureEnvelope(t, authorityPayload))
	require.NoError(t, err)
	_, err = solutionhost.AuthorityFromVerified(authorityCarrier.Document)
	require.NoError(t, err)
}

// Carrier refuses a payload no consumer could round-trip, so the render that
// produced it fails rather than the host that received it.
func TestCarrierRefusesWhatCannotBeRoundTripped(t *testing.T) {
	envelope := parseSigned(t, "presence").Envelope

	_, err := solutionhost.Carrier(nil, envelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)

	_, err = solutionhost.Carrier([]byte(`{"schema":"codefly/solution-host-binding/v1"}`), envelope)
	require.Error(t, err)

	// Declaration-order bytes of a real document: they parse, and they are not
	// canonical, so they are refused here rather than at the host.
	nonCanonical, err := json.Marshal(valid(t))
	require.NoError(t, err)
	_, err = solutionhost.Carrier(nonCanonical, envelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)

	payload, err := solutionhost.SignedPayload(valid(t))
	require.NoError(t, err)
	_, err = solutionhost.Carrier(payload, nil)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	_, err = solutionhost.Carrier(payload, json.RawMessage(`"not an object"`))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
}

// The whole signed kit runs through real offline signature verification.
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
				require.ErrorContains(t, err, shipped.Message)
				return
			}
			read := func() error {
				if shipped.Name == "authority" {
					_, err := solutionhost.VerifyDeliveredAuthority(context.Background(), signed, fixtureVerifier(t))
					return err
				}
				_, err := solutionhost.VerifyDelivered(context.Background(), signed, fixtureVerifier(t))
				return err
			}
			if shipped.Outcome == solutionhost.OutcomeRejected {
				require.ErrorContains(t, read(), shipped.Message)
				return
			}
			require.NoError(t, read())
		})
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
