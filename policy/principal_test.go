package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// TestPrincipal_Validate covers each Validate failure mode plus the
// happy path for each Kind. The deliberate redundancy between cases
// is the point — every error branch has a named test so a regression
// surfaces with the precise reason.
func TestPrincipal_Validate(t *testing.T) {
	tests := []struct {
		name      string
		p         *policy.Principal
		wantError bool
		errSubstr string
	}{
		{
			name:      "nil principal rejected",
			p:         nil,
			wantError: true,
			errSubstr: "nil principal",
		},
		{
			name:      "empty ID rejected",
			p:         &policy.Principal{Kind: policy.KindHuman, OrgID: "org-1"},
			wantError: true,
			errSubstr: "empty ID",
		},
		{
			name:      "empty Kind rejected",
			p:         &policy.Principal{ID: "p-1", OrgID: "org-1"},
			wantError: true,
			errSubstr: "empty Kind",
		},
		{
			name:      "unknown Kind rejected",
			p:         &policy.Principal{ID: "p-1", Kind: "robot", OrgID: "org-1"},
			wantError: true,
			errSubstr: "unknown Kind",
		},
		{
			name: "empty OrgID rejected for service",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindService,
			},
			wantError: true,
			errSubstr: "OrgID",
		},
		{
			name: "empty OrgID rejected for agent",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindAgent,
				AgentID: "x/y:z",
			},
			wantError: true,
			errSubstr: "OrgID",
		},
		{
			name: "empty OrgID OK for human (cross-org)",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindHuman, DisplayName: "antoine",
			},
			wantError: false,
		},
		{
			name: "agent without AgentID rejected",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindAgent, OrgID: "org-1",
			},
			wantError: true,
			errSubstr: "agent kind requires",
		},
		{
			name: "non-agent with AgentID rejected (drift guard)",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1",
				AgentID: "codefly.dev/auto-merge:0.1.0",
			},
			wantError: true,
			errSubstr: "AgentID set on non-agent",
		},
		{
			name: "valid human with org context",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1",
				DisplayName: "antoine",
			},
		},
		{
			name: "valid human without org context",
			p: &policy.Principal{
				ID: "p-1", Kind: policy.KindHuman,
				DisplayName: "antoine",
			},
		},
		{
			name: "valid service",
			p: &policy.Principal{
				ID: "p-2", Kind: policy.KindService, OrgID: "org-1",
				DisplayName: "ci-runner",
			},
		},
		{
			name: "valid agent",
			p: &policy.Principal{
				ID: "p-3", Kind: policy.KindAgent, OrgID: "org-1",
				AgentID:     "codefly.dev/auto-merge:0.1.0",
				DisplayName: "Auto Merge Bot",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if tc.wantError {
				require.Error(t, err)
				require.True(t, errors.Is(err, policy.ErrPrincipalInvalid),
					"error must wrap ErrPrincipalInvalid for callers using errors.Is; got %v", err)
				if tc.errSubstr != "" {
					require.Contains(t, err.Error(), tc.errSubstr,
						"error must mention the specific failure for log clarity")
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestPrincipal_IsExpired covers the three states: never-expires
// (zero time), not-yet-expired, and expired. The IsExpiredAt variant
// lets us drive the clock deterministically.
func TestPrincipal_IsExpired(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	t.Run("zero ExpiresAt never expires", func(t *testing.T) {
		p := &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "org"}
		require.False(t, p.IsExpiredAt(now), "zero time means perpetual credential")
	})

	t.Run("future ExpiresAt not expired", func(t *testing.T) {
		p := &policy.Principal{
			ID: "p", Kind: policy.KindHuman, OrgID: "org",
			ExpiresAt: now.Add(time.Hour),
		}
		require.False(t, p.IsExpiredAt(now))
	})

	t.Run("past ExpiresAt expired", func(t *testing.T) {
		p := &policy.Principal{
			ID: "p", Kind: policy.KindHuman, OrgID: "org",
			ExpiresAt: now.Add(-time.Second),
		}
		require.True(t, p.IsExpiredAt(now))
	})

	t.Run("exact ExpiresAt expired (boundary)", func(t *testing.T) {
		// At exactly the expiry instant the credential is no longer
		// valid — callers at this instant should already have refreshed.
		p := &policy.Principal{
			ID: "p", Kind: policy.KindHuman, OrgID: "org",
			ExpiresAt: now,
		}
		require.True(t, p.IsExpiredAt(now), "boundary must be inclusive of expiry")
	})

	t.Run("nil principal expired", func(t *testing.T) {
		var p *policy.Principal
		require.True(t, p.IsExpiredAt(now), "nil principal is treated as expired (defense)")
	})
}

// TestPrincipal_AsIdentity verifies the Identity map shape — the
// drift-prone surface where PDP rules read keys. Renaming a key here
// without coordinating with the PDP would silently default-deny;
// these tests pin the contract.
func TestPrincipal_AsIdentity(t *testing.T) {
	t.Run("nil principal returns nil map", func(t *testing.T) {
		var p *policy.Principal
		require.Nil(t, p.AsIdentity())
	})

	t.Run("human principal has core keys", func(t *testing.T) {
		p := &policy.Principal{
			ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1",
			DisplayName: "antoine", Token: "jwt.token.value",
		}
		got := p.AsIdentity()
		require.Equal(t, "p-1", got["principal_id"])
		require.Equal(t, policy.KindHuman, got["principal_kind"])
		require.Equal(t, "org-1", got["principal_org_id"])
		require.Equal(t, "antoine", got["principal_display_name"])
		require.Equal(t, "jwt.token.value", got["principal_token"])
		require.NotContains(t, got, "agent_id", "human must not have agent_id")
		require.NotContains(t, got, "delegation_chain", "no chain → key absent, not empty slice")
	})

	t.Run("agent principal has agent_id", func(t *testing.T) {
		p := &policy.Principal{
			ID: "p-3", Kind: policy.KindAgent, OrgID: "org-1",
			AgentID: "codefly.dev/auto-merge:0.1.0",
		}
		got := p.AsIdentity()
		require.Equal(t, "codefly.dev/auto-merge:0.1.0", got["agent_id"])
	})

	t.Run("delegation chain serialized as slice of maps", func(t *testing.T) {
		p := &policy.Principal{
			ID: "p-3", Kind: policy.KindAgent, OrgID: "org-1",
			AgentID: "codefly.dev/bot:0.1.0",
			DelegationChain: []policy.DelegationLink{
				{PrincipalID: "u-1", Kind: policy.KindHuman, DisplayName: "antoine", GrantID: "g-100"},
				{PrincipalID: "u-2", Kind: policy.KindHuman, DisplayName: "approver", GrantID: "g-101"},
			},
		}
		got := p.AsIdentity()
		chain, ok := got["delegation_chain"].([]map[string]any)
		require.True(t, ok, "delegation_chain must be a slice of maps")
		require.Len(t, chain, 2)
		require.Equal(t, "u-1", chain[0]["principal_id"])
		require.Equal(t, "g-100", chain[0]["grant_id"])
		require.Equal(t, "u-2", chain[1]["principal_id"])
	})

	t.Run("empty optional fields omitted", func(t *testing.T) {
		// Empty fields are omitted rather than included as empty
		// strings, so PDP rules can rely on key presence as a signal.
		p := &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "o"}
		got := p.AsIdentity()
		require.NotContains(t, got, "principal_display_name")
		require.NotContains(t, got, "principal_token")
		require.NotContains(t, got, "agent_id")
	})
}

// TestWithPrincipal_RoundTrip covers the context-stamp helpers. The
// "no principal" case returns nil distinct from a "zero principal"
// — PDP and audit branch on this distinction.
func TestWithPrincipal_RoundTrip(t *testing.T) {
	p := &policy.Principal{
		ID: "p-1", Kind: policy.KindHuman, OrgID: "org-1",
		DisplayName: "antoine",
	}

	t.Run("stamp and retrieve", func(t *testing.T) {
		ctx := policy.WithPrincipal(context.Background(), p)
		got := policy.PrincipalFrom(ctx)
		require.Same(t, p, got, "must return the same pointer; copies hide bugs")
	})

	t.Run("nil context tolerated", func(t *testing.T) {
		ctx := policy.WithPrincipal(nil, p) //nolint:staticcheck // intentional nil
		got := policy.PrincipalFrom(ctx)
		require.Same(t, p, got)
	})

	t.Run("empty context returns nil", func(t *testing.T) {
		got := policy.PrincipalFrom(context.Background())
		require.Nil(t, got, "no stamp → nil, distinct from a zero-value Principal")
	})

	t.Run("nil context returns nil", func(t *testing.T) {
		got := policy.PrincipalFrom(nil) //nolint:staticcheck // intentional nil
		require.Nil(t, got)
	})

	t.Run("overwrite replaces", func(t *testing.T) {
		ctx := policy.WithPrincipal(context.Background(), p)
		other := &policy.Principal{ID: "p-2", Kind: policy.KindAgent, OrgID: "org-1", AgentID: "codefly.dev/x:0.1.0"}
		ctx = policy.WithPrincipal(ctx, other)
		require.Same(t, other, policy.PrincipalFrom(ctx))
	})
}
