package policy_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// A host whose signing key lives in a key-management service that never exports
// it cannot pass an ed25519.PrivateKey. It injects the signing capability, and
// the token must be byte-identical to one minted from the material: same
// envelope, same signature over it. Only who holds the key moves.

type injectedSigner struct {
	private ed25519.PrivateKey
	calls   int
	opts    []crypto.SignerOpts
	err     error
}

func (s *injectedSigner) Public() crypto.PublicKey { return s.private.Public() }

func (s *injectedSigner) Sign(_ io.Reader, message []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.calls++
	s.opts = append(s.opts, opts)
	if s.err != nil {
		return nil, s.err
	}
	return ed25519.Sign(s.private, message), nil
}

// mintInput pins the clock and the id so two mints of the same input are
// byte-comparable; without that, the equality assertion below would be about
// the timestamp rather than about the signature.
func mintInput() policy.MintInput {
	issued := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	return policy.MintInput{
		Principal:  &policy.Principal{ID: "u-1", Kind: policy.KindHuman, OrgID: "org"},
		Action:     "github.merge_pr",
		Resource:   "repo:acme/widgets",
		AudienceID: "audience",
		TTL:        time.Minute,
		NowFunc:    func() time.Time { return issued },
		IDFunc:     func() string { return "01ARZ3NDEKTSV4RRFFQ69G5FA0" },
	}
}

func TestMintEd25519WithSignerMatchesMintFromTheKey(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	fromKey, authorizationFromKey, err := policy.MintEd25519(mintInput(), private)
	require.NoError(t, err)

	signer := &injectedSigner{private: private}
	fromSigner, authorizationFromSigner, err := policy.MintEd25519WithSigner(mintInput(), signer)
	require.NoError(t, err)

	require.Equal(t, fromKey, fromSigner,
		"the wire format is the contract; injecting the signer may not change a byte of it")
	require.Equal(t, authorizationFromKey, authorizationFromSigner)
	require.Equal(t, 1, signer.calls)
	require.Equal(t, []crypto.SignerOpts{crypto.Hash(0)}, signer.opts,
		"Ed25519 signs the envelope, never a digest")
}

func TestMintEd25519WithSignerRefusesByName(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	t.Run("nil signer", func(t *testing.T) {
		_, _, err := policy.MintEd25519WithSigner(mintInput(), nil)
		require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
		require.Contains(t, err.Error(), "nil signer")
	})

	t.Run("the signer is not ed25519", func(t *testing.T) {
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, keyErr)
		_, _, err := policy.MintEd25519WithSigner(mintInput(), key)
		require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
		require.Contains(t, err.Error(), "not an ed25519 key")
	})

	// An unreachable key service refuses the mint rather than returning a token
	// with an empty signature.
	t.Run("the signer fails", func(t *testing.T) {
		token, _, err := policy.MintEd25519WithSigner(mintInput(),
			&injectedSigner{private: private, err: errors.New("key service unavailable")})
		require.Error(t, err)
		require.Empty(t, token)
		require.Contains(t, err.Error(), "key service unavailable")
	})
}

// A short key is still refused on the material-taking entry point, which is the
// behaviour every existing caller relies on.
func TestMintEd25519StillRefusesAShortKey(t *testing.T) {
	_, _, err := policy.MintEd25519(mintInput(), ed25519.PrivateKey("too-short"))
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "must be 64 bytes")
}
