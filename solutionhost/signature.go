package solutionhost

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SchemaSignedV1 is the only signature-carrier schema this package reads.
const SchemaSignedV1 = "codefly/solution-host-signed/v1"

// SignedFileName is the conventional name of a delivered signed document.
//
// The carrier is JSON, not YAML, even under this extension. The signature
// covers the document's canonical bytes verbatim, and a YAML emitter folds a
// long scalar across lines — which would change those bytes and turn every
// delivered signature into a verification failure nobody can read. JSON has no
// folding, and a JSON object is valid YAML, so the carrier still travels
// through a pipeline that classifies delivery documents by this extension.
const SignedFileName = "solution-host-binding.signed.codefly.yaml"

// AlgorithmEd25519 is the only signature algorithm this package verifies.
// One algorithm, named in the document and checked against this constant: an
// algorithm field a verifier honours as written is how a signature ends up
// checked by whichever primitive the writer preferred.
const AlgorithmEd25519 = "ed25519"

var (
	// ErrUnsigned means a carrier is not a signed document this package reads —
	// a wrong schema, a missing signature, or a shape that is not the envelope.
	ErrUnsigned = errors.New("solution host document is not a signed document")

	// ErrSignature means the payload does not verify under the trust anchor the
	// caller supplied: an unknown key, a revoked key, a bad signature, or bytes
	// that are not the canonical encoding of the document they decode to.
	ErrSignature = errors.New("solution host document signature does not verify")

	// ErrNoAnchor means the caller supplied no trust anchor. It is a separate
	// error from a failed verification because the two call for opposite
	// responses: a failed signature is a document to refuse, and a missing
	// anchor is a verifier to fix. Verifying against an empty anchor would
	// otherwise refuse every sound document and read as an attack.
	ErrNoAnchor = errors.New("solution host document verification needs a trust anchor")
)

// A key id identifies a key the verifier already holds. The pattern admits an
// identifier and nothing that could be dereferenced: no scheme, no slash, no
// whitespace, no PEM. A document never says where to find a key, so there is
// nothing here for a resolver to follow.
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,126}[A-Za-z0-9]$`)

// Signature is the detached signature over a document's canonical bytes.
//
// It names which key signed, never where to find one. There is no field for a
// URL, a certificate, a certificate chain or key material, and the carrier
// decodes strictly — so a document that tries to nominate its own trusted key
// is refused when it is parsed rather than silently ignored. The verifier's
// trust in a key id comes from a bootstrap independent of everything that can
// write a delivery document.
type Signature struct {
	// Algorithm must be AlgorithmEd25519.
	Algorithm string `json:"algorithm"`

	// KeyID identifies a key the verifier already trusts.
	KeyID string `json:"key_id"`

	// Value is the base64 (standard encoding) signature over Document.
	Value string `json:"value"`
}

// Signed is a document plus the detached signature over its canonical bytes.
//
// Document is kept verbatim because that is what was signed. Re-encoding it
// from a parsed form would produce bytes the signature does not cover, and the
// only honest answer then is that nothing verifies.
type Signed struct {
	// Schema is the carrier's own version.
	Schema string `json:"schema"`

	// Document is the canonical bytes that were signed, verbatim. The document
	// TYPE is not a field here: it is the schema inside these bytes, so it is
	// covered by the signature. An outer type field would be an unsigned claim
	// about a signed payload, which is worse than no field at all.
	Document json.RawMessage `json:"document"`

	// Signature is the detached signature over Document.
	Signature Signature `json:"signature"`
}

// Anchor is the trust anchor a caller supplies: the keys it already trusts, and
// the key ids it no longer does. A document never nominates its own key, so
// this is the whole of what a verification is decided against.
//
// Rotation is two entries in Keys for as long as both are in use, then one.
// Revocation is a key id in Revoked, which is refused even while the key is
// still in Keys and still verifies arithmetically — so revoking is one edit
// that holds, rather than an edit that only holds if the key is also removed
// everywhere it was copied to.
type Anchor struct {
	// Keys are the public keys this caller trusts, by key id.
	Keys map[string]ed25519.PublicKey

	// Revoked are key ids that must no longer be trusted, whatever Keys says.
	Revoked []string
}

// ParseSigned decodes a signature carrier. Decoding is strict: an unknown field
// is an error, which is what refuses a document that carries a key, a
// certificate or a URL alongside its signature instead of quietly dropping it.
func ParseSigned(data []byte) (*Signed, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var signed Signed
	if err := decoder.Decode(&signed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsigned, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: the carrier holds more than one document", ErrUnsigned)
	}
	if signed.Schema != SchemaSignedV1 {
		return nil, fmt.Errorf("%w: schema %q (this Core reads %q)", ErrSchema, signed.Schema, SchemaSignedV1)
	}
	if len(signed.Document) == 0 {
		return nil, fmt.Errorf("%w: the carrier holds no document", ErrUnsigned)
	}
	return &signed, nil
}

// VerifyPresence verifies a signed presence document under the caller's anchor
// and returns it. A document that does not verify yields nothing: there is no
// partially-trusted form, and the parsed contents of an unverified payload are
// exactly what a forged document would want a caller to read.
func VerifyPresence(signed *Signed, anchor Anchor) (*SolutionHostBinding, error) {
	return verify(signed, anchor, Parse, SchemaPresenceV2, func(document *SolutionHostBinding) ([]byte, error) {
		return document.CanonicalBytes()
	})
}

// VerifyAuthority verifies a signed authority document under the caller's
// anchor and returns it. Validating it against an envelope, and matching it to
// a presence document and a build, are separate steps: see
// (*AuthorityDocument).ValidateAgainst and Activate.
func VerifyAuthority(signed *Signed, anchor Anchor) (*AuthorityDocument, error) {
	return verify(signed, anchor, ParseAuthority, SchemaAuthorityV1, func(document *AuthorityDocument) ([]byte, error) {
		return document.CanonicalBytes()
	})
}

// verify holds the whole of what a signature decides, for either document type.
//
// The order matters. Revocation is checked before the key is looked up and
// before the signature is computed, so a revoked key cannot be rehabilitated by
// a signature that happens to verify. The canonical round-trip is checked last,
// after the signature: a payload whose bytes are not the canonical encoding of
// the document they decode to is refused even when the signature over those
// bytes is genuine, because a signer and a host that disagree about which bytes
// represent the document disagree about what was approved.
func verify[T any](
	signed *Signed,
	anchor Anchor,
	parse func([]byte) (*T, error),
	schema string,
	canonical func(*T) ([]byte, error),
) (*T, error) {
	if signed == nil {
		return nil, fmt.Errorf("%w: no signed document", ErrUnsigned)
	}
	if len(anchor.Keys) == 0 {
		return nil, fmt.Errorf("%w: the anchor holds no key", ErrNoAnchor)
	}
	if signed.Signature.Algorithm != AlgorithmEd25519 {
		return nil, fmt.Errorf("%w: algorithm %q is not %q", ErrSignature, signed.Signature.Algorithm, AlgorithmEd25519)
	}
	keyID := signed.Signature.KeyID
	if !keyIDPattern.MatchString(keyID) {
		// A key id that is a URL, a PEM block or a path is a document trying to
		// say where its key lives. The refusal names that, because "unknown
		// key" would send an operator looking for a key to add.
		return nil, fmt.Errorf("%w: key id %q is not an identifier; a document names a key the verifier already trusts and never where to find one",
			ErrSignature, keyID)
	}
	if slices.Contains(anchor.Revoked, keyID) {
		return nil, fmt.Errorf("%w: key %q is revoked", ErrSignature, keyID)
	}
	key, trusted := anchor.Keys[keyID]
	if !trusted {
		return nil, fmt.Errorf("%w: no trusted key %q", ErrSignature, keyID)
	}
	// ed25519.Verify panics on a key that is not PublicKeySize bytes, and the
	// key id comes from the untrusted document — so one malformed anchor entry
	// would turn every document naming it into a crash of the verifying process
	// rather than a refusal, on demand for anyone who learns that key id.
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: trusted key %q is %d bytes, not %d", ErrSignature, keyID, len(key), ed25519.PublicKeySize)
	}
	value, err := base64.StdEncoding.DecodeString(signed.Signature.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not base64: %v", ErrSignature, err)
	}
	if !ed25519.Verify(key, signed.Document, value) {
		return nil, fmt.Errorf("%w: payload does not verify under key %q", ErrSignature, keyID)
	}
	document, err := parse(signed.Document)
	if err != nil {
		return nil, err
	}
	// The document type is bound by the signature because the schema string is
	// inside the signed bytes. Checking it here is what stops a genuinely
	// signed document of one type being accepted where the other was asked for.
	if got := documentSchema(signed.Document); got != schema {
		return nil, fmt.Errorf("%w: the signed payload declares %q, not %q", ErrSchema, got, schema)
	}
	encoded, err := canonical(document)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, signed.Document) {
		return nil, fmt.Errorf("%w: the signed payload is not the canonical encoding of the document it decodes to", ErrSignature)
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
// document. They exist so that a signer in the reviewed delivery pipeline — the
// only place a signing key belongs — never has to reimplement the canonical
// encoding, and so that "what was signed" has one answer.
//
// This package deliberately ships no signer. A signer here would put one in
// every binary that imports core, and the authority to sign a document that
// grants authority is not something a library hands out.
func SignedPayload(document *SolutionHostBinding) ([]byte, error) {
	return document.CanonicalBytes()
}

// SignedPayloadFor returns the exact bytes a signer must sign for a validated
// authority document.
func SignedPayloadFor(document *AuthorityDocument) ([]byte, error) {
	return document.CanonicalBytes()
}

// Carrier assembles the carrier for a payload a signer has already signed. It
// takes bytes and a signature, never a key: assembling the delivered form is
// not the same authority as producing the signature, and only the first belongs
// in a library.
func Carrier(payload []byte, signature Signature) (*Signed, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: no payload", ErrUnsigned)
	}
	if signature.Algorithm != AlgorithmEd25519 {
		return nil, fmt.Errorf("%w: algorithm %q is not %q", ErrSignature, signature.Algorithm, AlgorithmEd25519)
	}
	if !keyIDPattern.MatchString(signature.KeyID) {
		return nil, fmt.Errorf("%w: key id %q is not an identifier", ErrSignature, signature.KeyID)
	}
	if strings.TrimSpace(signature.Value) == "" {
		return nil, fmt.Errorf("%w: no signature value", ErrSignature)
	}
	if _, err := base64.StdEncoding.DecodeString(signature.Value); err != nil {
		return nil, fmt.Errorf("%w: signature is not base64: %v", ErrSignature, err)
	}
	return &Signed{Schema: SchemaSignedV1, Document: slices.Clone(payload), Signature: signature}, nil
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
	return json.Marshal(signed)
}
