package solutionhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

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
	// a missing document, a missing envelope, or an invalid carrier shape.
	ErrUnsigned = errors.New("solution host document is not a signed document")

	// ErrNotCanonical means a payload is not the canonical encoding of the
	// document it decodes to. It is refused even when the signature over those
	// bytes is genuine: a signer and a host that disagree about which bytes
	// represent the document disagree about what was approved, and the host's
	// stored digest would not match the payload that was attested.
	ErrNotCanonical = errors.New("solution host document payload is not its canonical encoding")
)

// Signed carries a canonical document and the DSSE envelope attesting it.
// Verification is offline, using only the Go standard library and the host's
// pinned public keys. No third-party service or carrier-supplied trust material
// participates. SignEnvelope uses a caller-held Signer to produce the envelope.
//
// ParseSigned only decodes the carrier. VerifyDelivered and
// VerifyDeliveredAuthority verify it before returning a document for admission.
type Signed struct {
	// Document contains the canonical document bytes verbatim. Its schema and
	// the envelope's payloadType must agree, and its bytes must be identical
	// to the envelope's decoded payload. Re-encoding is not verification.
	Document json.RawMessage `json:"document"`

	// Envelope is a DSSE JSON object with exactly one signature.
	Envelope json.RawMessage `json:"envelope"`
}

// ParseSigned decodes a signature carrier. Decoding is strict: an unknown field
// is an error, which is what refuses a carrier that ships a key, a certificate
// or a URL alongside its envelope instead of quietly dropping it. Verifier
// decodes and checks the envelope; this parser checks the carrier shape.
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
	if err := signed.validate(); err != nil {
		return nil, err
	}
	return &signed, nil
}

func (signed *Signed) validate() error {
	if len(bytes.TrimSpace(signed.Document)) == 0 {
		return fmt.Errorf("%w: the carrier holds no document", ErrUnsigned)
	}
	// An envelope that is absent, empty or JSON null is no attestation. The shape
	// parser requires its presence; Verifier checks its contents.
	trimmed := bytes.TrimSpace(signed.Envelope)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("%w: the carrier holds no signature envelope", ErrUnsigned)
	}
	// An object, because an envelope is one. Refusing the other JSON shapes keeps
	// a single wire form: a base64 string of an envelope would also decode as
	// valid JSON here, and two accepted shapes is two code paths downstream.
	if trimmed[0] != '{' {
		return fmt.Errorf("%w: the signature envelope must be a JSON object", ErrUnsigned)
	}
	return nil
}

// PresenceFromVerified turns payload bytes the caller HAS ALREADY VERIFIED into
// a presence document.
//
// This function verifies no signature and checks no pinned key. A caller
// that reaches it without verifying the envelope over these exact bytes has
// authenticated nothing. A successful return here is not evidence that
// anything was signed.
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
	return fromVerified(payload, Parse, SchemaPresenceV4, func(document *SolutionHostBinding) ([]byte, error) {
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
	return fromVerified(payload, ParseAuthority, SchemaAuthorityV4, func(document *AuthorityDocument) ([]byte, error) {
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

// documentSchema reads the document kind. Invalid JSON or a missing schema
// returns an empty string, which no supported document kind accepts.
func documentSchema(payload []byte) string {
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return ""
	}
	return header.Schema
}

// SignedPayload returns the canonical payload bytes for a validated
// presence document, and SignedPayloadFor does the same for an authority
// document. They exist so the signing pipeline never reimplements the canonical
// encoding, and so that "what was signed" has one answer on both sides.
//
// Pass these bytes to SignEnvelope with a platform-owned Signer.
func SignedPayload(document *SolutionHostBinding) ([]byte, error) {
	return document.CanonicalBytes()
}

// SignedPayloadFor returns the canonical payload bytes for a validated
// authority document.
func SignedPayloadFor(document *AuthorityDocument) ([]byte, error) {
	return document.CanonicalBytes()
}

// Carrier assembles the delivered form from a payload and the envelope attesting
// it. SignEnvelope produces an envelope using a caller-held key. Carrier checks the
// document shape; acceptance still requires the host's pinned-key Verifier.
func Carrier(payload []byte, envelope json.RawMessage) (*Signed, error) {
	signed := &Signed{
		Document: bytes.Clone(payload),
		Envelope: bytes.Clone(envelope),
	}
	if err := signed.validate(); err != nil {
		return nil, err
	}
	// The payload must be canonical for SOMETHING, or the carrier is built
	// around bytes no consumer can round-trip. Checking it here fails the
	// render that produced it rather than the host that received it.
	if _, presence := PresenceFromVerified(payload); presence != nil {
		if _, authority := AuthorityFromVerified(payload); authority != nil {
			if _, endpoints := EndpointDeliveryFromVerified(payload); endpoints != nil {
				return nil, fmt.Errorf("%w: the payload is neither a canonical presence document (%v), nor a canonical authority document (%v), nor a canonical endpoint delivery (%v)",
					ErrUnsigned, presence, authority, endpoints)
			}
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
	if err := signed.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(signed)
}
