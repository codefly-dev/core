package solutionhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SchemaSignedV1 is the only signature-carrier schema this package reads.
const SchemaSignedV1 = "codefly/solution-host-signed/v1"

// SignedFileName is the conventional name of a delivered signed document.
//
// The carrier is JSON, not YAML, even under this extension. The verified
// payload is the document's canonical bytes verbatim, and a YAML emitter folds
// a long scalar across lines — which would change those bytes and turn every
// delivered signature into a verification failure nobody can read. JSON has no
// folding, and a JSON object is valid YAML, so the carrier still travels
// through a pipeline that classifies delivery documents by this extension.
const SignedFileName = "solution-host-binding.signed.codefly.yaml"

var (
	// ErrUnsigned means a carrier is not a signed document this package reads —
	// a wrong schema, a missing payload, a missing bundle, or a shape that is
	// not the envelope.
	ErrUnsigned = errors.New("solution host document is not a signed document")

	// ErrNotCanonical means a payload is not the canonical encoding of the
	// document it decodes to. It is refused even when the signature over those
	// bytes is genuine: a signer and a host that disagree about which bytes
	// represent the document disagree about what was approved, and the host's
	// stored digest would not match the payload that was attested.
	ErrNotCanonical = errors.New("solution host document payload is not its canonical encoding")
)

// Signed is a document and the signature bundle attesting it.
//
// # What core does and does not do here
//
// Core does NOT verify the bundle. It holds no trust root, no identity policy
// and no key material, and it interprets nothing inside Bundle. Signing is
// keyless: a release is signed by CI over its workload's OIDC identity, the
// signature is by an ephemeral key certified with that identity, and
// VERIFICATION IS AN IDENTITY ALLOWLIST — the signing repository, workflow
// path, ref pattern and OIDC issuer — checked against a trust root the verifier
// holds, never a key the document names. Rotation reduces to trust-root
// updates. None of that belongs in a library every binary imports: a trust root
// and an identity policy are deployment configuration, and the component that
// owns them is the one that verifies.
//
// So the division is: the caller verifies the bundle over Document with a
// Sigstore verifier and its own identity policy, and THEN hands the verified
// bytes to PresenceFromVerified or AuthorityFromVerified, which own the
// document half — strict parse, the schema that names the document type, and
// the canonical round-trip.
//
// The bundle's certificate is EVIDENCE, checked against the caller's identity
// allowlist. It is never read as authority. That distinction is the whole
// reason core refuses to interpret the bundle at all: a library that parsed a
// certificate out of a delivery document would be one short step from trusting
// what it found there.
type Signed struct {
	// Schema is the carrier's own version.
	Schema string `json:"schema"`

	// Document is the canonical bytes that were signed, verbatim. The document
	// TYPE is not a field here: it is the schema inside these bytes, so it is
	// covered by the signature. An outer type field would be an unsigned claim
	// about a signed payload, which is worse than no field at all.
	//
	// Verbatim matters. Re-encoding these bytes from a parsed form would
	// produce bytes the signature does not cover, and the only honest answer
	// then is that nothing verifies.
	Document json.RawMessage `json:"document"`

	// Bundle is the Sigstore bundle over Document, opaque to this package. It
	// is required: a carrier with no bundle is a document, not a signed one,
	// and letting it through would make "signed" a shape rather than a claim.
	Bundle json.RawMessage `json:"bundle"`
}

// ParseSigned decodes a signature carrier. Decoding is strict: an unknown field
// is an error, which is what refuses a carrier that ships a key, a certificate
// or a URL alongside its bundle instead of quietly dropping it. Bundle is the
// one field whose contents this package does not look at, and that boundary is
// deliberate rather than incidental.
func ParseSigned(data []byte) (*Signed, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var signed Signed
	if err := decoder.Decode(&signed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsigned, err)
	}
	// Decoder.More() is an array/object iteration check, not an end-of-input
	// check: a carrier followed by a stray "]" or "}" passes it even though
	// the whole input is not valid JSON. Requiring a second decode to return
	// io.EOF is what actually establishes that nothing follows.
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: the carrier holds more than one document", ErrUnsigned)
		}
		return nil, fmt.Errorf("%w: trailing input after the carrier: %v", ErrUnsigned, err)
	}
	if signed.Schema != SchemaSignedV1 {
		return nil, fmt.Errorf("%w: schema %q (this Core reads %q)", ErrSchema, signed.Schema, SchemaSignedV1)
	}
	if err := signed.validate(); err != nil {
		return nil, err
	}
	return &signed, nil
}

func (signed *Signed) validate() error {
	if len(bytes.TrimSpace(signed.Document)) == 0 {
		return fmt.Errorf("%w: the carrier holds no document", ErrUnsigned)
	}
	// A bundle that is absent, empty or JSON null is no attestation. Core does
	// not look inside it, but "there is one" is checkable and is the difference
	// between a signed document and a document.
	trimmed := bytes.TrimSpace(signed.Bundle)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("%w: the carrier holds no signature bundle", ErrUnsigned)
	}
	// An object, because a bundle is one. Refusing the other JSON shapes keeps
	// a single wire form: a base64 string of a bundle would also decode as
	// valid JSON here, and two accepted shapes is two code paths downstream.
	if trimmed[0] != '{' {
		return fmt.Errorf("%w: the signature bundle must be a JSON object", ErrUnsigned)
	}
	return nil
}

// PresenceFromVerified turns payload bytes the caller HAS ALREADY VERIFIED into
// a presence document.
//
// The name is the contract. This function verifies no signature, checks no
// certificate identity and holds no trust root — a caller that reaches it
// without having verified the bundle over exactly these bytes has authenticated
// nothing, and a successful return here is not evidence that anything was
// signed.
//
// What it does own is the document half, and all three parts matter:
//
//   - strict parse, so an unknown field is a version step rather than a
//     silently ignored intention;
//   - the schema, which names the document TYPE and is inside the signed bytes,
//     so a verified authority payload cannot be read as a presence document;
//   - the canonical round-trip, which refuses a payload that is not the
//     canonical encoding of the document it decodes to. That is refused even
//     when the attestation over those bytes is genuine, because a signer and a
//     host that disagree about which bytes represent the document disagree
//     about what was approved — and the digest the host stores would not match
//     the bytes that were attested.
func PresenceFromVerified(payload []byte) (*SolutionHostBinding, error) {
	return fromVerified(payload, Parse, SchemaPresenceV2, func(document *SolutionHostBinding) ([]byte, error) {
		return document.CanonicalBytes()
	})
}

// AuthorityFromVerified turns payload bytes the caller has already verified
// into an authority document. Everything PresenceFromVerified says applies.
//
// Validating it against an envelope, and matching it to a presence document and
// a build, are separate steps: see (*AuthorityDocument).ValidateAgainst and
// Activate.
func AuthorityFromVerified(payload []byte) (*AuthorityDocument, error) {
	return fromVerified(payload, ParseAuthority, SchemaAuthorityV1, func(document *AuthorityDocument) ([]byte, error) {
		return document.CanonicalBytes()
	})
}

func fromVerified[T any](
	payload []byte,
	parse func([]byte) (*T, error),
	schema string,
	canonical func(*T) ([]byte, error),
) (*T, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, fmt.Errorf("%w: no payload", ErrUnsigned)
	}
	document, err := parse(payload)
	if err != nil {
		return nil, err
	}
	if got := documentSchema(payload); got != schema {
		return nil, fmt.Errorf("%w: the payload declares %q, not %q", ErrSchema, got, schema)
	}
	encoded, err := canonical(document)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, payload) {
		return nil, ErrNotCanonical
	}
	return document, nil
}

// documentSchema reads the schema string out of a payload that has already
// parsed, so it cannot fail in a way a caller must handle.
func documentSchema(payload []byte) string {
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return ""
	}
	return header.Schema
}

// SignedPayload returns the exact bytes a signer must sign for a validated
// presence document, and SignedPayloadFor does the same for an authority
// document. They exist so the signing pipeline never reimplements the canonical
// encoding, and so that "what was signed" has one answer on both sides.
//
// This package deliberately ships no signer. Signing is CI's, over a workload
// identity, and the authority to sign a document that grants authority is not
// something a library hands to every binary that imports it.
func SignedPayload(document *SolutionHostBinding) ([]byte, error) {
	return document.CanonicalBytes()
}

// SignedPayloadFor returns the exact bytes a signer must sign for a validated
// authority document.
func SignedPayloadFor(document *AuthorityDocument) ([]byte, error) {
	return document.CanonicalBytes()
}

// Carrier assembles the delivered form from a payload and the bundle attesting
// it. It takes the bundle as bytes and never produces one: assembling the
// carrier is not the same authority as attesting it, and only the first belongs
// in a library.
func Carrier(payload []byte, bundle json.RawMessage) (*Signed, error) {
	signed := &Signed{
		Schema:   SchemaSignedV1,
		Document: bytes.Clone(payload),
		Bundle:   bytes.Clone(bundle),
	}
	if err := signed.validate(); err != nil {
		return nil, err
	}
	// The payload must be canonical for SOMETHING, or the carrier is built
	// around bytes no consumer can round-trip. Checking it here fails the
	// render that produced it rather than the host that received it.
	if _, presence := PresenceFromVerified(payload); presence != nil {
		if _, authority := AuthorityFromVerified(payload); authority != nil {
			return nil, fmt.Errorf("%w: the payload is neither a canonical presence document (%v) nor a canonical authority document (%v)",
				ErrUnsigned, presence, authority)
		}
	}
	return signed, nil
}

// MarshalSigned renders a carrier for delivery. The output is JSON, for the
// reason SignedFileName gives.
func MarshalSigned(signed *Signed) ([]byte, error) {
	if signed == nil {
		return nil, fmt.Errorf("%w: no signed document", ErrUnsigned)
	}
	if signed.Schema != SchemaSignedV1 {
		return nil, fmt.Errorf("%w: schema %q (this Core writes %q)", ErrSchema, signed.Schema, SchemaSignedV1)
	}
	if err := signed.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(signed)
}
