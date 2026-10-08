package workcontext

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// An issuer whose key lives in a key-management service that never exports it
// cannot hand this package an ed25519.PrivateKey. It injects the signing
// capability instead, and the capability it mints must be byte-identical to one
// minted from the material — same algorithm, same deterministic protobuf
// payload, same wire format. Only who holds the key moves.
//
// These tests are inside the package because seal is the last guard before a
// token is handed out, and the external routes to it fail earlier.

// remoteSigner stands in for a key-management service: it can sign and it can
// name its public half, and it cannot produce the private key.
type remoteSigner struct {
	private ed25519.PrivateKey
	calls   int
	// opts records what the caller asked for, because Ed25519 signs the message
	// and a signer handed a digest would produce something no verifier accepts.
	opts []crypto.SignerOpts
	err  error
}

func (s *remoteSigner) Public() crypto.PublicKey { return s.private.Public() }

func (s *remoteSigner) Sign(_ io.Reader, message []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.calls++
	s.opts = append(s.opts, opts)
	if s.err != nil {
		return nil, s.err
	}
	return ed25519.Sign(s.private, message), nil
}

func soundClaims() *basev0.WorkContextV1 {
	return &basev0.WorkContextV1{
		Typ: Typ, Algorithm: Algorithm, KeyId: "k",
		Issuer: "https://issuer.test", Audience: "aud",
		Nonce: "n", ReplayPolicy: ReplayIdempotent,
		TenantId: "t", OwnerPrincipalId: "p", TaskId: "task", SessionId: "s",
		AuthorityScopes: []*basev0.WorkScopeV1{{ResourceKind: "record", Actions: []string{"read"}}},
		Seal: &basev0.WorkSealV1{
			PrincipalEpoch: 1, InstallationId: "inst",
			InstallationRevision: 1,
			BuildIncarnation:     fixtureIncarnationPointer(),
			ImageDigest:          fixtureDigestPointer(),
		},
	}
}

func TestSealProducesTheSameTokenFromAnInjectedSignerAsFromTheKey(t *testing.T) {
	key := testSigningKey(t)
	fromMaterial := &Authority{Issuer: "https://issuer.test", KeyID: "k", Key: key}
	signer := &remoteSigner{private: key}
	fromSigner := &Authority{Issuer: "https://issuer.test", KeyID: "k", Signer: signer}

	material, _, err := fromMaterial.seal(soundClaims())
	require.NoError(t, err)
	injected, _, err := fromSigner.seal(soundClaims())
	require.NoError(t, err)

	require.Equal(t, material, injected,
		"the wire format is the contract; injecting the signer may not change a byte of it")
	require.Equal(t, 1, signer.calls)
	require.Equal(t, []crypto.SignerOpts{crypto.Hash(0)}, signer.opts,
		"Ed25519 signs the message, never a digest")
}

func TestSealRefusesASignerWhosePublicKeyIsNotEd25519(t *testing.T) {
	_, notEd25519, err := generateNonEd25519(t)
	require.NoError(t, err)
	authority := &Authority{Issuer: "https://issuer.test", KeyID: "k", Signer: notEd25519}

	_, _, err = authority.seal(soundClaims())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not an ed25519 key")
}

// Accepting both and preferring one would let a caller that set the wrong field
// sign with a key it did not mean to — and the result would verify.
func TestSealRefusesBothAKeyAndASigner(t *testing.T) {
	key := testSigningKey(t)
	authority := &Authority{
		Issuer: "https://issuer.test", KeyID: "k",
		Key: key, Signer: &remoteSigner{private: key},
	}

	_, _, err := authority.seal(soundClaims())
	require.Error(t, err)
	require.Contains(t, err.Error(), "one signing capability")
}

func TestSealRefusesWhenNeitherIsSet(t *testing.T) {
	_, _, err := (&Authority{Issuer: "https://issuer.test", KeyID: "k"}).seal(soundClaims())
	require.Error(t, err)
	require.Contains(t, err.Error(), "neither Key nor Signer is set")
}

// A key service that is unreachable must refuse the mint, not hand out a
// capability with an empty or short signature.
func TestSealRefusesWhenTheSignerFails(t *testing.T) {
	signer := &remoteSigner{private: testSigningKey(t), err: errors.New("key service unavailable")}
	authority := &Authority{Issuer: "https://issuer.test", KeyID: "k", Signer: signer}

	token, _, err := authority.seal(soundClaims())
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "key service unavailable")
}

// generateNonEd25519 returns a signer whose public half is not Ed25519.
func generateNonEd25519(t *testing.T) (crypto.PublicKey, crypto.Signer, error) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return key.Public(), key, nil
}
