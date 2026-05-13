package testharness

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/codefly-dev/core/policy"
)

// PrincipalBuilder constructs test Principals fluently. The default
// (zero-value Builder) gives a valid human principal in org "test-org"
// with a generated ID — useful when the test doesn't care who, just
// that there IS a principal.
//
// Usage:
//
//	p := testharness.NewPrincipalBuilder().AsAgent("auto-merge", "0.1.0").Build()
//	ctx := policy.WithPrincipal(context.Background(), p)
type PrincipalBuilder struct {
	id          string
	kind        string
	orgID       string
	agentID     string
	displayName string
	token       string
	expiresAt   time.Time
	chain       []policy.DelegationLink
}

// principalCounter lets the zero builder generate unique IDs without
// reaching for crypto/rand. Tests typically don't care about the ID
// shape; they care that two builders produce distinct IDs. atomic so
// parallel tests don't collide.
var principalCounter atomic.Uint64

// NewPrincipalBuilder returns a builder with sensible defaults: a
// human principal in "test-org" with a unique generated ID.
func NewPrincipalBuilder() *PrincipalBuilder {
	n := principalCounter.Add(1)
	return &PrincipalBuilder{
		id:          fmt.Sprintf("test-principal-%d", n),
		kind:        policy.KindHuman,
		orgID:       "test-org",
		displayName: fmt.Sprintf("test-user-%d", n),
	}
}

// WithID overrides the auto-generated ID. Use when the test must
// assert a specific principal_id appearing in audit / PDP calls.
func (b *PrincipalBuilder) WithID(id string) *PrincipalBuilder {
	b.id = id
	return b
}

// WithOrg sets the organization. Defaults to "test-org".
func (b *PrincipalBuilder) WithOrg(orgID string) *PrincipalBuilder {
	b.orgID = orgID
	return b
}

// WithDisplayName sets the human-readable name surfaced in audit /
// approval UI.
func (b *PrincipalBuilder) WithDisplayName(name string) *PrincipalBuilder {
	b.displayName = name
	return b
}

// WithToken sets the credential string. Tests of token-aware paths
// (PDP that re-verifies caveats, audit that logs the token id) need
// this to be a real-shaped token; tests of pure auth flow can leave
// it as the empty default.
func (b *PrincipalBuilder) WithToken(tok string) *PrincipalBuilder {
	b.token = tok
	return b
}

// ExpiringIn sets ExpiresAt to now+d. Use this rather than
// WithExpiresAt when the test only cares about "expires soon" /
// "expires far in the future" relative timing.
func (b *PrincipalBuilder) ExpiringIn(d time.Duration) *PrincipalBuilder {
	b.expiresAt = time.Now().Add(d)
	return b
}

// WithExpiresAt sets the absolute expiry. Used by tests that drive
// IsExpiredAt with a fixed clock.
func (b *PrincipalBuilder) WithExpiresAt(t time.Time) *PrincipalBuilder {
	b.expiresAt = t
	return b
}

// AsHuman switches kind to human, clearing any previously-set agent
// fields. Default; rarely called explicitly.
func (b *PrincipalBuilder) AsHuman() *PrincipalBuilder {
	b.kind = policy.KindHuman
	b.agentID = ""
	return b
}

// AsService switches kind to service. Use for test fixtures
// representing CI runners, deployment bots without per-version
// identity, etc.
func (b *PrincipalBuilder) AsService() *PrincipalBuilder {
	b.kind = policy.KindService
	b.agentID = ""
	return b
}

// AsAgent switches kind to agent and sets AgentID to the canonical
// "publisher/name:version" form. Tests should pass non-empty name +
// version; we fill in publisher as "test.codefly.dev" if not
// specified via WithAgentPublisher.
func (b *PrincipalBuilder) AsAgent(name, version string) *PrincipalBuilder {
	if name == "" {
		panic("AsAgent: name must be non-empty")
	}
	if version == "" {
		panic("AsAgent: version must be non-empty")
	}
	b.kind = policy.KindAgent
	b.agentID = fmt.Sprintf("test.codefly.dev/%s:%s", name, version)
	if b.displayName == "" || b.kind == policy.KindHuman {
		b.displayName = name
	}
	return b
}

// WithAgentID overrides the auto-built AgentID. Use when the test
// needs an exact identifier (e.g. matching a real plugin manifest).
func (b *PrincipalBuilder) WithAgentID(id string) *PrincipalBuilder {
	b.agentID = id
	if b.kind != policy.KindAgent {
		b.kind = policy.KindAgent
	}
	return b
}

// DelegatedFrom appends a link to the delegation chain. Multiple
// calls build a chain in the order they were called (oldest first).
//
// Example: a sub-agent acting under user antoine's authority who
// herself delegated through Mind:
//
//	NewPrincipalBuilder().AsAgent("merger", "0.1.0").
//	    DelegatedFrom("user-antoine", policy.KindHuman, "antoine", "grant-1").
//	    DelegatedFrom("agent-mind", policy.KindAgent, "Mind", "grant-2").
//	    Build()
func (b *PrincipalBuilder) DelegatedFrom(principalID, kind, displayName, grantID string) *PrincipalBuilder {
	b.chain = append(b.chain, policy.DelegationLink{
		PrincipalID: principalID,
		Kind:        kind,
		DisplayName: displayName,
		GrantID:     grantID,
	})
	return b
}

// Build produces the Principal. Panics on Validate failure — tests
// should fail loud if the builder produced something nonsensical.
func (b *PrincipalBuilder) Build() *policy.Principal {
	p := &policy.Principal{
		ID:              b.id,
		Kind:            b.kind,
		OrgID:           b.orgID,
		AgentID:         b.agentID,
		DisplayName:     b.displayName,
		Token:           b.token,
		ExpiresAt:       b.expiresAt,
		DelegationChain: b.chain,
	}
	if err := p.Validate(); err != nil {
		panic(fmt.Sprintf("PrincipalBuilder.Build produced invalid principal: %v", err))
	}
	return p
}
