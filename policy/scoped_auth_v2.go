package policy

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// =====================================================================
// v2-ed25519 token format — public-key signing
// =====================================================================
//
// **Why v2 alongside v1.** v1-hmac uses a SHARED secret between
// gateway and plugin: the gateway needs the secret to sign;
// plugins need the same secret to verify. That's fine for
// single-host setups (host process spawns the plugin and shares
// secret via env), but breaks down when:
//
//   - Gateway and plugin run on different machines (multi-host
//     deployments). Distributing the shared secret securely is
//     a coordination problem.
//   - Multiple gateways in different orgs sign tokens that the
//     SAME plugin must verify (federated environments).
//   - Operators want to rotate signing keys without re-deploying
//     plugins. With HMAC, rotation requires the plugin to learn
//     the new secret in lock-step with the gateway.
//
// Public-key signing solves all three. The gateway holds the
// PRIVATE key (kept on the gateway host); plugins hold the
// PUBLIC key (distributable, no secrecy required).
//
// **Why ed25519 over RSA / ECDSA.** ed25519 is the modern
// default: small keys (32 bytes public, 64 bytes private),
// fast verify (~50µs/op), no parameter choices to get wrong,
// constant-time signing. Standard library: `crypto/ed25519`.
//
// **Why not full Biscuit yet.** The proposal mentioned Biscuit
// for v2. Biscuit's compelling features are first-/third-party
// caveats with Datalog evaluation and offline attenuation by
// holders. We get the public-key benefit (the load-bearing
// motivation) from plain ed25519 without dragging in
// biscuit-go's Datalog runtime. Operators who genuinely need
// attenuable bearer tokens add v3-biscuit later — same fmt-tag
// dispatch pattern, additive change.
//
// **Wire format.** Same shape as v1 (`<envelope>.<sig>`), with
// `fmt: "v2-ed25519"` in the envelope and a 64-byte ed25519
// signature in the second segment. The base64url encoding is
// identical so debuggability tools that work for v1 work for v2.

// scopedAuthFormatV2 is the format tag for ed25519-signed tokens.
const scopedAuthFormatV2 = "v2-ed25519"

// MintEd25519 produces a v2 token signed with the supplied
// ed25519 private key. The verifier needs the matching public
// key (extractable from the private key as `priv.Public()`).
//
// Same input semantics as Mint (v1): Principal must be valid,
// Action must be non-empty, TTL must be > 0. MaxUses defaults
// to 1.
//
// **Signing key length.** ed25519.PrivateKey is 64 bytes
// (32 seed + 32 public). Generate via:
//
//	pub, priv, err := ed25519.GenerateKey(crypto/rand.Reader)
//
// Caller stores priv on the gateway and distributes pub to
// plugins (env var, JWKS endpoint, or static config).
func MintEd25519(input MintInput, privateKey ed25519.PrivateKey) (string, *ScopedAuthorization, error) {
	if input.Principal == nil {
		return "", nil, fmt.Errorf("%w: nil principal", ErrScopedAuthInvalid)
	}
	if err := input.Principal.Validate(); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrScopedAuthInvalid, err)
	}
	if input.Action == "" {
		return "", nil, fmt.Errorf("%w: empty action", ErrScopedAuthInvalid)
	}
	if input.TTL <= 0 {
		return "", nil, fmt.Errorf("%w: TTL must be > 0", ErrScopedAuthInvalid)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", nil, fmt.Errorf("%w: ed25519 private key must be %d bytes (got %d)", ErrScopedAuthInvalid, ed25519.PrivateKeySize, len(privateKey))
	}

	now := time.Now
	if input.NowFunc != nil {
		now = input.NowFunc
	}
	idFn := NewULID
	if input.IDFunc != nil {
		idFn = input.IDFunc
	}

	maxUses := input.MaxUses
	if maxUses < 0 {
		return "", nil, fmt.Errorf("%w: max_uses must be >= 0", ErrScopedAuthInvalid)
	}
	if maxUses == 0 {
		maxUses = 1
	}

	issuedAt := now()
	sa := &ScopedAuthorization{
		ID:             idFn(),
		Format:         scopedAuthFormatV2,
		PrincipalID:    input.Principal.ID,
		PrincipalKind:  input.Principal.Kind,
		PrincipalOrgID: input.Principal.OrgID,
		Action:         input.Action,
		Resource:       input.Resource,
		IssuedAtUnix:   issuedAt.Unix(),
		ExpiresAtUnix:  issuedAt.Add(input.TTL).Unix(),
		MaxUses:        maxUses,
		AudienceID:     input.AudienceID,
		CatalogDigest:  input.CatalogDigest,
		RequestDigest:  input.RequestDigest,
		Caveats:        input.Caveats,
	}

	envelope, err := json.Marshal(sa)
	if err != nil {
		return "", nil, fmt.Errorf("%w: marshal: %v", ErrScopedAuthInvalid, err)
	}
	signature := ed25519.Sign(privateKey, envelope)
	encoded := base64.RawURLEncoding.EncodeToString(envelope) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
	return encoded, sa, nil
}

// VerifyEd25519 decodes + verifies a v2 token using the supplied
// ed25519 public key. Token format mismatch (e.g. v1-hmac passed
// here) returns an error — use TokenVerifier for dual-format
// dispatch.
//
// All other expectations behave identically to Verify (v1):
// time bounds, audience/action/resource/principal matching,
// caveat verification.
func VerifyEd25519(token string, expect VerifyExpectations, publicKey ed25519.PublicKey) (*ScopedAuthorization, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrScopedAuthInvalid)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: ed25519 public key must be %d bytes (got %d)", ErrScopedAuthInvalid, ed25519.PublicKeySize, len(publicKey))
	}

	dot := strings.IndexByte(token, '.')
	if dot < 0 || dot == len(token)-1 {
		return nil, fmt.Errorf("%w: malformed token", ErrScopedAuthInvalid)
	}
	envB64, sigB64 := token[:dot], token[dot+1:]

	envelope, err := base64.RawURLEncoding.DecodeString(envB64)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope base64: %v", ErrScopedAuthInvalid, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("%w: sig base64: %v", ErrScopedAuthInvalid, err)
	}

	// ed25519 signatures are always 64 bytes; reject early to
	// surface format mismatches (e.g. somebody tried to verify
	// a v1-hmac signature with this function).
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature length %d (expected %d for ed25519); wrong format?",
			ErrScopedAuthInvalid, len(sig), ed25519.SignatureSize)
	}

	if !ed25519.Verify(publicKey, envelope, sig) {
		return nil, fmt.Errorf("%w: signature verification failed", ErrScopedAuthInvalid)
	}

	var sa ScopedAuthorization
	if err := json.Unmarshal(envelope, &sa); err != nil {
		return nil, fmt.Errorf("%w: envelope json: %v", ErrScopedAuthInvalid, err)
	}

	if sa.Format != scopedAuthFormatV2 {
		return nil, fmt.Errorf("%w: VerifyEd25519 received fmt=%q (expected %q); use TokenVerifier for dual-format support",
			ErrScopedAuthInvalid, sa.Format, scopedAuthFormatV2)
	}

	if err := checkScopedAuthClaims(&sa, expect); err != nil {
		return nil, err
	}
	return &sa, nil
}

// =====================================================================
// TokenVerifier — dual-format dispatch
// =====================================================================
//
// **Why a verifier type.** Hosts mid-migration mint v1 tokens for
// some plugins and v2 tokens for others; plugins must accept
// both. A single Verify call that dispatches on the envelope's
// fmt tag keeps plugin code clean.
//
// **Key rotation.** PublicKeys is a list — each ed25519 token is
// verified against ALL configured keys; first match wins. Lets
// operators rotate by:
//
//   1. Add the new public key to plugins (PublicKeys = [old, new]).
//   2. Switch the gateway's PRIVATE key to the new pair.
//   3. After old tokens expire (typically minutes), drop the old
//      public key (PublicKeys = [new]).
//
// Same pattern as JWT key rotation via JWKS.

// TokenVerifier verifies scoped-auth tokens, dispatching on the
// envelope's `fmt:` tag. Holds the keys for each format it
// accepts. Zero value rejects every token (no keys configured) —
// callers must explicitly opt in via WithHMACSecret /
// WithEd25519PublicKey.
type TokenVerifier struct {
	// HMACSecret accepts v1-hmac tokens. Empty disables v1.
	HMACSecret []byte

	// PublicKeys are the ed25519 public keys that accept
	// v2-ed25519 tokens. Multi-key for rotation; first match
	// wins. Empty disables v2.
	PublicKeys []ed25519.PublicKey
}

// NewTokenVerifier returns a verifier with no keys. Callers chain
// WithHMACSecret / WithEd25519PublicKey.
func NewTokenVerifier() *TokenVerifier {
	return &TokenVerifier{}
}

// WithHMACSecret enables v1-hmac verification.
func (v *TokenVerifier) WithHMACSecret(secret []byte) *TokenVerifier {
	v.HMACSecret = secret
	return v
}

// WithEd25519PublicKey enables v2-ed25519 verification with the
// given public key. Calling multiple times accumulates keys for
// rotation.
func (v *TokenVerifier) WithEd25519PublicKey(key ed25519.PublicKey) *TokenVerifier {
	if len(key) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("policy.WithEd25519PublicKey: key must be %d bytes (got %d)", ed25519.PublicKeySize, len(key)))
	}
	v.PublicKeys = append(v.PublicKeys, key)
	return v
}

// Verify dispatches on the envelope's fmt tag. Returns an error
// if the token's format isn't supported by any configured key.
func (v *TokenVerifier) Verify(token string, expect VerifyExpectations) (*ScopedAuthorization, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrScopedAuthInvalid)
	}
	// Cheap pre-decode of the envelope to read the format tag
	// without verifying the signature. Saves doing two passes
	// when the host is configured for one format and the token
	// is the other.
	dot := strings.IndexByte(token, '.')
	if dot < 0 {
		return nil, fmt.Errorf("%w: malformed token (no separator)", ErrScopedAuthInvalid)
	}
	envBytes, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return nil, fmt.Errorf("%w: envelope base64: %v", ErrScopedAuthInvalid, err)
	}
	var probe struct {
		Format string `json:"fmt"`
	}
	if err := json.Unmarshal(envBytes, &probe); err != nil {
		return nil, fmt.Errorf("%w: envelope json: %v", ErrScopedAuthInvalid, err)
	}

	switch probe.Format {
	case scopedAuthFormat: // v1-hmac
		if len(v.HMACSecret) == 0 {
			return nil, fmt.Errorf("%w: token is v1-hmac but no HMAC secret configured", ErrScopedAuthInvalid)
		}
		return Verify(token, expect, v.HMACSecret)
	case scopedAuthFormatV2:
		if len(v.PublicKeys) == 0 {
			return nil, fmt.Errorf("%w: token is v2-ed25519 but no public keys configured", ErrScopedAuthInvalid)
		}
		// Try each public key — first match wins. Common case
		// is one key; rotation periods have two.
		var lastErr error
		for _, pub := range v.PublicKeys {
			sa, err := VerifyEd25519(token, expect, pub)
			if err == nil {
				return sa, nil
			}
			lastErr = err
		}
		return nil, lastErr
	default:
		return nil, fmt.Errorf("%w: unknown format %q", ErrScopedAuthInvalid, probe.Format)
	}
}

// =====================================================================
// Helpers
// =====================================================================

// checkScopedAuthClaims is the post-signature-verify branch: time
// bounds + claim matching + caveat verification. Shared by Verify
// (v1) and VerifyEd25519 (v2) so the rules are identical
// regardless of signing format.
func checkScopedAuthClaims(sa *ScopedAuthorization, expect VerifyExpectations) error {
	now := expect.Now
	if now.IsZero() {
		now = time.Now()
	}

	expiresAt := time.Unix(sa.ExpiresAtUnix, 0)
	issuedAt := time.Unix(sa.IssuedAtUnix, 0)
	if now.After(expiresAt.Add(scopedAuthClockSkew)) {
		return fmt.Errorf("%w: expired at %s (now %s)", ErrScopedAuthInvalid, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if issuedAt.After(now.Add(scopedAuthClockSkew)) {
		return fmt.Errorf("%w: issued in the future (clock skew exceeds tolerance)", ErrScopedAuthInvalid)
	}

	if expect.Action != "" && sa.Action != expect.Action {
		return fmt.Errorf("%w: action mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.Action, expect.Action)
	}
	// NOTE: this primitive intentionally skips the resource check when the
	// expectation is empty (symmetric with Action above) — it is the low-level
	// signature/claims verifier. Enforcing that a resource-scoped token is only
	// accepted when the CALL actually carries a matching resource is the Guard's
	// job (policyguard.CallTool), which knows both the token and the call.
	if expect.Resource != "" && sa.Resource != expect.Resource {
		return fmt.Errorf("%w: resource mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.Resource, expect.Resource)
	}
	if expect.Audience != "" && sa.AudienceID != expect.Audience {
		return fmt.Errorf("%w: audience mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.AudienceID, expect.Audience)
	}
	if expect.CatalogDigest != "" && sa.CatalogDigest != expect.CatalogDigest {
		return fmt.Errorf("%w: catalog digest mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.CatalogDigest, expect.CatalogDigest)
	}
	if expect.RequestDigest != "" && sa.RequestDigest != expect.RequestDigest {
		return fmt.Errorf("%w: request digest mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.RequestDigest, expect.RequestDigest)
	}
	if expect.PrincipalID != "" && sa.PrincipalID != expect.PrincipalID {
		return fmt.Errorf("%w: principal mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.PrincipalID, expect.PrincipalID)
	}
	if expect.PrincipalKind != "" && sa.PrincipalKind != expect.PrincipalKind {
		return fmt.Errorf("%w: principal kind mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.PrincipalKind, expect.PrincipalKind)
	}
	if expect.OrganizationID != "" && sa.PrincipalOrgID != expect.OrganizationID {
		return fmt.Errorf("%w: organization mismatch (token=%q, want=%q)", ErrScopedAuthInvalid, sa.PrincipalOrgID, expect.OrganizationID)
	}

	for key, value := range sa.Caveats {
		verifier, ok := expect.CaveatVerifiers[key]
		if !ok {
			return fmt.Errorf("%w: unknown caveat %q (defense-in-depth: unknown caveats deny)", ErrScopedAuthInvalid, key)
		}
		if err := verifier(value); err != nil {
			return fmt.Errorf("%w: caveat %q rejected: %v", ErrScopedAuthInvalid, key, err)
		}
	}
	for _, key := range expect.RequiredCaveats {
		if _, ok := sa.Caveats[key]; !ok {
			return fmt.Errorf("%w: required caveat %q is missing", ErrScopedAuthInvalid, key)
		}
		if _, ok := expect.CaveatVerifiers[key]; !ok {
			return fmt.Errorf("%w: required caveat %q has no registered verifier", ErrScopedAuthInvalid, key)
		}
	}

	return nil
}

// =====================================================================
// Errors
// =====================================================================

// ErrUnknownTokenFormat is returned by TokenVerifier.Verify when
// the envelope's `fmt:` tag isn't recognized.
var ErrUnknownTokenFormat = errors.New("unknown token format")
