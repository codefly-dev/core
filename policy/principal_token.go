package policy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// principal_token.go — wire encoding for the principal claim that
// codefly host hands to a spawned plugin via env var, and that the
// plugin presents back as gRPC metadata on outgoing calls.
//
// **What format is this.** Until M6 lands Biscuit, the token is a
// base64-URL-encoded JSON document. Three reasons we don't use a
// real JWT yet:
//
//  1. JWT minting requires an asymmetric signing key plumbed from
//     saas-starter to codefly host. Production wiring will use it
//     (RS256 with JWKS already exposed); but plumbing the signer is
//     M3-side work that the M3 SaasPDP requires anyway.
//  2. Tests need to construct tokens cheaply. Plain JSON-base64 is
//     trivially constructible in test fixtures (NewTestToken below).
//  3. Biscuit (M6) replaces this entirely. Designing a complex
//     intermediate signed format would be churn we throw away.
//
// **Security note.** Without a signature, the token is forgeable by
// anyone with access to the env var. This is INTENTIONAL for the
// development scaffolding phase, with explicit gates:
//
//   - Production code paths that require unforgeability must call
//     mintSignedPrincipalToken (M3 phase 2) which uses the saas-
//     starter signer. Until that lands, SaasPDP refuses unsigned
//     tokens unless CODEFLY_PDP_ALLOW_UNSIGNED=1 is set (which the
//     M3 SaasPDP will check; see core/policy/pdp_saas.go).
//   - Tests can pass FakePDP which doesn't verify; the token is
//     just the carrier of identity claims for the assert layer.
//   - The plugin process is sandboxed, so an attacker who'd need
//     to forge the token would already need to be inside the
//     sandbox — the credential isn't load-bearing in that
//     attack model.

// principalEnvelope is the wire shape for a principal token.
// Mirrors Principal but as a serializable struct with
// explicit tags. Renaming fields here MUST be coordinated with the
// plugin-side decoder.
type principalEnvelope struct {
	ID              string                  `json:"id"`
	Kind            string                  `json:"kind"`
	OrgID           string                  `json:"org_id,omitempty"`
	AgentID         string                  `json:"agent_id,omitempty"`
	DisplayName     string                  `json:"display_name,omitempty"`
	IssuedAtUnix    int64                   `json:"iat"`
	ExpiresAtUnix   int64                   `json:"exp,omitempty"`
	DelegationChain []principalChainElement `json:"chain,omitempty"`
	Format          string                  `json:"fmt"` // "v1-unsigned" until M6
}

type principalChainElement struct {
	PrincipalID string `json:"principal_id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name,omitempty"`
	GrantID     string `json:"grant_id,omitempty"`
}

// principalTokenFormat is the format string embedded in every token
// so a future Biscuit token can coexist with v1-unsigned during
// migration. Decoders branch on this field.
const principalTokenFormat = "v1-unsigned"

// encodePrincipalToken produces the env-var token from a Principal.
// Returns base64url(json(envelope)) — URL-safe so quoting / shell
// escaping doesn't mangle.
//
// The Principal must be Validate()'d by the caller; we don't re-
// validate here because the loader already did upstream. Returns
// an error only on JSON marshal failure, which would indicate a
// programmer error in the Principal type itself.
func EncodePrincipalToken(p *Principal) (string, error) {
	if p == nil {
		return "", fmt.Errorf("encodePrincipalToken: nil principal")
	}

	env := principalEnvelope{
		ID:           p.ID,
		Kind:         p.Kind,
		OrgID:        p.OrgID,
		AgentID:      p.AgentID,
		DisplayName:  p.DisplayName,
		IssuedAtUnix: time.Now().Unix(),
		Format:       principalTokenFormat,
	}
	if !p.ExpiresAt.IsZero() {
		env.ExpiresAtUnix = p.ExpiresAt.Unix()
	}
	for _, link := range p.DelegationChain {
		env.DelegationChain = append(env.DelegationChain, principalChainElement{
			PrincipalID: link.PrincipalID,
			Kind:        link.Kind,
			DisplayName: link.DisplayName,
			GrantID:     link.GrantID,
		})
	}

	raw, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal principal envelope: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodePrincipalToken is the plugin-side inverse of
// encodePrincipalToken. Exported because the plugin-side
// interceptor (in agents/agents.go) needs it. Returns an error if
// the encoding is malformed, the format isn't recognized, or the
// envelope fails the Principal Validate (e.g. agent without
// agent_id).
//
// Does NOT verify a signature — see the comment at the top of this
// file. The SaasPDP (M3) is responsible for re-validating against
// saas-starter for any decision-making.
func DecodePrincipalToken(s string) (*Principal, error) {
	if s == "" {
		return nil, fmt.Errorf("decode principal token: empty")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode principal token: base64: %w", err)
	}
	var env principalEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode principal token: json: %w", err)
	}
	if env.Format != principalTokenFormat {
		// Future-proof: if Biscuit is "v2-biscuit" later, an updated
		// decoder branches here. Today the only valid format is v1.
		return nil, fmt.Errorf("decode principal token: unknown format %q", env.Format)
	}

	p := &Principal{
		ID:          env.ID,
		Kind:        env.Kind,
		OrgID:       env.OrgID,
		AgentID:     env.AgentID,
		DisplayName: env.DisplayName,
		Token:       s, // store the original token so downstream PDP can re-verify
	}
	if env.ExpiresAtUnix > 0 {
		p.ExpiresAt = time.Unix(env.ExpiresAtUnix, 0)
	}
	for _, link := range env.DelegationChain {
		p.DelegationChain = append(p.DelegationChain, DelegationLink{
			PrincipalID: link.PrincipalID,
			Kind:        link.Kind,
			DisplayName: link.DisplayName,
			GrantID:     link.GrantID,
		})
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("decode principal token: %w", err)
	}
	if p.IsExpired() {
		return nil, fmt.Errorf("decode principal token: expired at %s", p.ExpiresAt.Format(time.RFC3339))
	}
	return p, nil
}
