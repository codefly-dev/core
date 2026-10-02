//go:build ignore

// gen_solutionhost_fixtures regenerates the signed conformance fixtures in
// solutionhost/testdata/signed and the trust anchor they verify against.
//
//	go run scripts/gen_solutionhost_fixtures.go
//
// Run it whenever a presence or authority fixture changes: the signatures cover
// those documents' canonical bytes, so editing one invalidates its signature,
// and TestSignedFixturesReachTheirOutcome fails until this is rerun.
//
// # Why this is a program and not a package
//
// This file carries the fixture signing keys, and it is excluded from every
// build by its `ignore` tag. The solutionhost package ships the public keys
// and the already-signed bytes, and no signer at all — a library that could
// sign an authority document would put the authority to grant authority in
// every binary that imports core. Signing belongs to the reviewed delivery
// pipeline, and this program is the fixtures' stand-in for it.
//
// # The keys are not secret and must never be reused
//
// They are derived from the fixed seeds below so the fixtures regenerate
// byte-for-byte, which is what makes a change to them show up as a diff rather
// than as churn. Every private key this program derives is therefore public, in
// this file, in git history. Nothing outside these fixtures may trust them.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/solutionhost"
)

// Seeds for the fixture keys. Public by construction — see the file comment.
const (
	activeSeed    = "codefly solution host conformance fixtures: active key"
	revokedSeed   = "codefly solution host conformance fixtures: revoked key"
	untrustedSeed = "codefly solution host conformance fixtures: untrusted key"
)

func keyFrom(seed string) ed25519.PrivateKey {
	material := sha256.Sum256([]byte(seed))
	return ed25519.NewKeyFromSeed(material[:])
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen_solutionhost_fixtures:", err)
		os.Exit(1)
	}
}

func run() error {
	root := filepath.Join("solutionhost", "testdata", "signed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	active := keyFrom(activeSeed)
	revoked := keyFrom(revokedSeed)
	untrusted := keyFrom(untrustedSeed)

	// The anchor holds the active key and the revoked one, and does NOT hold
	// the untrusted one. That is what separates the two negative cases: a key
	// the anchor never had, and a key it has and no longer trusts.
	anchor := map[string]any{
		"_comment": "Fixture trust anchor. Public keys only; the private keys live in scripts/gen_solutionhost_fixtures.go and are public by construction. " +
			"The revoked key is deliberately still in keys: revoking has to hold even when the key is present and still verifies.",
		"keys": map[string]string{
			solutionhost.FixtureKeyID:        base64.StdEncoding.EncodeToString(active.Public().(ed25519.PublicKey)),
			solutionhost.FixtureRevokedKeyID: base64.StdEncoding.EncodeToString(revoked.Public().(ed25519.PublicKey)),
		},
		"revoked": []string{solutionhost.FixtureRevokedKeyID},
	}
	if err := writeJSON(filepath.Join(root, "anchor.json"), anchor); err != nil {
		return err
	}

	presence, err := solutionhost.Parse(mustRead(solutionhost.DocumentTypePresence, "valid"))
	if err != nil {
		return fmt.Errorf("presence fixture: %w", err)
	}
	presencePayload, err := solutionhost.SignedPayload(presence)
	if err != nil {
		return err
	}
	authority, err := solutionhost.ParseAuthority(mustRead(solutionhost.DocumentTypeAuthority, "valid"))
	if err != nil {
		return fmt.Errorf("authority fixture: %w", err)
	}
	authorityPayload, err := solutionhost.SignedPayloadFor(authority)
	if err != nil {
		return err
	}

	// The accepted pair.
	if err := sign(root, "presence", presencePayload, active, solutionhost.FixtureKeyID); err != nil {
		return err
	}
	if err := sign(root, "authority", authorityPayload, active, solutionhost.FixtureKeyID); err != nil {
		return err
	}
	// A genuine signature by a key the anchor does not hold.
	if err := sign(root, "untrusted-key", presencePayload, untrusted, "fixture-untrusted"); err != nil {
		return err
	}
	// A genuine signature by a key the anchor holds and has revoked.
	if err := sign(root, "revoked-key", presencePayload, revoked, solutionhost.FixtureRevokedKeyID); err != nil {
		return err
	}
	// A genuine signature over bytes that parse to the same document but are
	// not its canonical encoding: plain json.Marshal of the struct, which emits
	// keys in Go DECLARATION order rather than name order and leaves every
	// collection in delivery order. That is precisely what a signer that
	// reached for encoding/json instead of CanonicalBytes would produce, and
	// the malleability the round-trip check closes.
	//
	// Indenting the canonical bytes would not work: json.Marshal compacts a
	// json.RawMessage when the carrier is written, so the whitespace would be
	// gone and only the signature would be left wrong. Key order survives
	// compaction, which is why this is the shape used.
	declarationOrder, err := json.Marshal(presence)
	if err != nil {
		return err
	}
	if bytes.Equal(declarationOrder, presencePayload) {
		return fmt.Errorf("the non-canonical fixture is canonical after all; it would prove nothing")
	}
	if err := sign(root, "non-canonical", declarationOrder, active, solutionhost.FixtureKeyID); err != nil {
		return err
	}
	// A key id that says where to fetch a key. Signed genuinely, so the refusal
	// is the key id and not the signature.
	if err := signRaw(root, "key-id-is-a-url", presencePayload, active,
		"https://keys.obin.example/solution-host/2026-10.pub", nil); err != nil {
		return err
	}
	// A carrier that ships a public key beside its signature. Strict decoding
	// refuses the field, so this never reaches a signature check at all.
	if err := signRaw(root, "nominates-key", presencePayload, active, solutionhost.FixtureKeyID,
		map[string]any{"public_key": base64.StdEncoding.EncodeToString(active.Public().(ed25519.PublicKey))}); err != nil {
		return err
	}
	return nil
}

func mustRead(documentType solutionhost.DocumentType, name string) []byte {
	data, err := solutionhost.FixtureDocument(documentType, name)
	if err != nil {
		panic(err)
	}
	return data
}

// sign writes a well-formed carrier through solutionhost.Carrier, so every
// accepted fixture is assembled by the same code a delivery pipeline uses.
func sign(root, name string, payload []byte, key ed25519.PrivateKey, keyID string) error {
	signed, err := solutionhost.Carrier(payload, solutionhost.Signature{
		Algorithm: solutionhost.AlgorithmEd25519,
		KeyID:     keyID,
		Value:     base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	encoded, err := solutionhost.MarshalSigned(signed)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return write(filepath.Join(root, name+".json"), encoded)
}

// signRaw assembles a carrier the library would refuse to build — an
// un-identifier key id, or an extra field. It goes around Carrier on purpose:
// these fixtures exist so a consumer can pin the refusal, and a fixture the
// library cannot emit is exactly the one worth shipping.
func signRaw(root, name string, payload []byte, key ed25519.PrivateKey, keyID string, extra map[string]any) error {
	signature := map[string]any{
		"algorithm": solutionhost.AlgorithmEd25519,
		"key_id":    keyID,
		"value":     base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
	}
	for field, value := range extra {
		signature[field] = value
	}
	// Compact, like MarshalSigned, and NOT MarshalIndent: indenting re-indents
	// the embedded payload, which would change the bytes the signature covers
	// and make these fixtures fail on the signature rather than on the thing
	// they exist to pin.
	encoded, err := json.Marshal(map[string]any{
		"schema":    solutionhost.SchemaSignedV1,
		"document":  json.RawMessage(payload),
		"signature": signature,
	})
	if err != nil {
		return err
	}
	return write(filepath.Join(root, name+".json"), encoded)
}

func writeJSON(file string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return write(file, append(encoded, '\n'))
}

func write(file string, data []byte) error {
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", file)
	return nil
}
