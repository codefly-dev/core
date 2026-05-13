package policy_test

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func mustGenerateEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
	require.NoError(t, err)
	return pub, priv
}

// =====================================================================
// MintEd25519 / VerifyEd25519
// =====================================================================

func TestMintEd25519_RoundTrip(t *testing.T) {
	pub, priv := mustGenerateEd25519(t)
	p := &policy.Principal{ID: "u-1", Kind: policy.KindHuman, OrgID: "org"}

	encoded, sa, err := policy.MintEd25519(policy.MintInput{
		Principal:  p,
		Action:     "github.merge_pr",
		Resource:   "repo:codefly/x",
		AudienceID: "audience",
		TTL:        time.Minute,
	}, priv)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
	require.Equal(t, "v2-ed25519", sa.Format)

	got, err := policy.VerifyEd25519(encoded, policy.VerifyExpectations{
		Action:      "github.merge_pr",
		Resource:    "repo:codefly/x",
		Audience:    "audience",
		PrincipalID: "u-1",
	}, pub)
	require.NoError(t, err)
	require.Equal(t, "v2-ed25519", got.Format)
	require.Equal(t, sa.ID, got.ID)
}

func TestMintEd25519_InvalidPrincipal_Errors(t *testing.T) {
	_, priv := mustGenerateEd25519(t)
	_, _, err := policy.MintEd25519(policy.MintInput{
		Action: "x", TTL: time.Minute,
	}, priv)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}

func TestMintEd25519_BadKeyLength_Errors(t *testing.T) {
	p := &policy.Principal{ID: "u", Kind: policy.KindHuman}
	_, _, err := policy.MintEd25519(policy.MintInput{
		Principal: p, Action: "x", TTL: time.Minute,
	}, ed25519.PrivateKey([]byte("too-short")))
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "private key")
}

func TestVerifyEd25519_WrongPublicKey_Rejected(t *testing.T) {
	_, signingPriv := mustGenerateEd25519(t)
	wrongPub, _ := mustGenerateEd25519(t)

	encoded, _, err := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, signingPriv)
	require.NoError(t, err)

	_, err = policy.VerifyEd25519(encoded, policy.VerifyExpectations{}, wrongPub)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyEd25519_TamperedEnvelope_Rejected(t *testing.T) {
	pub, priv := mustGenerateEd25519(t)
	encoded, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, priv)

	// Flip a byte in the envelope.
	tampered := flipChar(encoded[5]) + encoded[1:]
	tampered = encoded[:5] + tampered[:len(tampered)-len(encoded[5:])]
	require.NotEqual(t, encoded, tampered)

	_, err := policy.VerifyEd25519(tampered, policy.VerifyExpectations{}, pub)
	require.Error(t, err)
}

func TestVerifyEd25519_TamperedSignature_Rejected(t *testing.T) {
	pub, priv := mustGenerateEd25519(t)
	encoded, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, priv)

	dot := strings.IndexByte(encoded, '.')
	require.Greater(t, dot, 0)
	tampered := encoded[:dot+1] + flipChar(encoded[dot+1]) + encoded[dot+2:]

	_, err := policy.VerifyEd25519(tampered, policy.VerifyExpectations{}, pub)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}

func TestVerifyEd25519_V1Token_RejectedWithFormatHint(t *testing.T) {
	// Mint a v1-hmac token; try to verify with VerifyEd25519.
	// Should reject loudly with a format-mismatch hint pointing
	// to TokenVerifier.
	secret := policy.NewSpawnSecret()
	pub, _ := mustGenerateEd25519(t)
	v1Token, _, err := policy.Mint(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, secret)
	require.NoError(t, err)

	_, err = policy.VerifyEd25519(v1Token, policy.VerifyExpectations{}, pub)
	require.Error(t, err)
}

func TestVerifyEd25519_BadKeyLength_Errors(t *testing.T) {
	_, err := policy.VerifyEd25519("anything.here", policy.VerifyExpectations{},
		ed25519.PublicKey([]byte("short")))
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "public key")
}

func TestVerifyEd25519_ClaimsChecked_SameAsV1(t *testing.T) {
	// Sanity: the post-signature claim checks (action mismatch,
	// audience mismatch, expiry) are shared between v1 and v2.
	// Spot-check action mismatch.
	pub, priv := mustGenerateEd25519(t)
	encoded, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x.y", TTL: time.Minute,
	}, priv)

	_, err := policy.VerifyEd25519(encoded, policy.VerifyExpectations{
		Action: "different.action",
	}, pub)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "action mismatch")
}

// =====================================================================
// TokenVerifier dual-format dispatch
// =====================================================================

func TestTokenVerifier_V1Token_VerifiedWithHMACSecret(t *testing.T) {
	secret := policy.NewSpawnSecret()
	v1Token, _, err := policy.Mint(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, secret)
	require.NoError(t, err)

	verifier := policy.NewTokenVerifier().WithHMACSecret(secret)
	sa, err := verifier.Verify(v1Token, policy.VerifyExpectations{})
	require.NoError(t, err)
	require.Equal(t, "v1-hmac", sa.Format)
}

func TestTokenVerifier_V2Token_VerifiedWithPublicKey(t *testing.T) {
	pub, priv := mustGenerateEd25519(t)
	v2Token, _, err := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, priv)
	require.NoError(t, err)

	verifier := policy.NewTokenVerifier().WithEd25519PublicKey(pub)
	sa, err := verifier.Verify(v2Token, policy.VerifyExpectations{})
	require.NoError(t, err)
	require.Equal(t, "v2-ed25519", sa.Format)
}

func TestTokenVerifier_DualFormat_AcceptsBoth(t *testing.T) {
	// Migration scenario: plugin must accept both v1 (legacy)
	// and v2 (new). Verifier configured with both keys.
	secret := policy.NewSpawnSecret()
	pub, priv := mustGenerateEd25519(t)

	verifier := policy.NewTokenVerifier().
		WithHMACSecret(secret).
		WithEd25519PublicKey(pub)

	v1, _, _ := policy.Mint(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, secret)
	v2, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, priv)

	saV1, err := verifier.Verify(v1, policy.VerifyExpectations{})
	require.NoError(t, err)
	require.Equal(t, "v1-hmac", saV1.Format)

	saV2, err := verifier.Verify(v2, policy.VerifyExpectations{})
	require.NoError(t, err)
	require.Equal(t, "v2-ed25519", saV2.Format)
}

func TestTokenVerifier_UnknownFormat_Rejected(t *testing.T) {
	// Build a token with an unknown fmt tag.
	rawJSON := []byte(`{"id":"x","fmt":"v999-future","action":"a","iat":1,"exp":99999999999}`)
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON) + ".sig"

	verifier := policy.NewTokenVerifier().
		WithHMACSecret(policy.NewSpawnSecret())

	_, err := verifier.Verify(encoded, policy.VerifyExpectations{})
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "v999-future")
}

func TestTokenVerifier_NoKeyForFormat_Rejected(t *testing.T) {
	// Verifier configured with HMAC only; v2 token presented.
	pub, priv := mustGenerateEd25519(t)
	v2Token, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, priv)
	_ = pub

	verifier := policy.NewTokenVerifier().
		WithHMACSecret(policy.NewSpawnSecret())

	_, err := verifier.Verify(v2Token, policy.VerifyExpectations{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no public keys configured")
}

func TestTokenVerifier_KeyRotation_AcceptsBothOldAndNew(t *testing.T) {
	// Rotation: verifier holds [oldPub, newPub]. Tokens signed
	// with EITHER private key verify successfully.
	oldPub, oldPriv := mustGenerateEd25519(t)
	newPub, newPriv := mustGenerateEd25519(t)

	verifier := policy.NewTokenVerifier().
		WithEd25519PublicKey(oldPub).
		WithEd25519PublicKey(newPub)

	signedOld, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, oldPriv)
	signedNew, _, _ := policy.MintEd25519(policy.MintInput{
		Principal: &policy.Principal{ID: "u", Kind: policy.KindHuman},
		Action:    "x", TTL: time.Minute,
	}, newPriv)

	_, err := verifier.Verify(signedOld, policy.VerifyExpectations{})
	require.NoError(t, err, "old key still in rotation list: must verify")

	_, err = verifier.Verify(signedNew, policy.VerifyExpectations{})
	require.NoError(t, err, "new key in rotation list: must verify")
}

func TestTokenVerifier_BadPublicKeyLength_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.NewTokenVerifier().WithEd25519PublicKey(ed25519.PublicKey([]byte("too-short")))
	})
}

func TestTokenVerifier_EmptyToken_Rejected(t *testing.T) {
	verifier := policy.NewTokenVerifier().WithHMACSecret(policy.NewSpawnSecret())
	_, err := verifier.Verify("", policy.VerifyExpectations{})
	require.Error(t, err)
}

func TestTokenVerifier_MalformedToken_Rejected(t *testing.T) {
	verifier := policy.NewTokenVerifier().WithHMACSecret(policy.NewSpawnSecret())
	for _, bad := range []string{"no-dot", "no-base64", "!!!"} {
		_, err := verifier.Verify(bad, policy.VerifyExpectations{})
		require.Error(t, err, "malformed: %q", bad)
	}
}
