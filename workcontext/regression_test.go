package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// A verifier accepts a capability for Skew past its expiry, so a caller can
// legitimately hold a *Verified that has already expired. Exchanging it used
// to clamp the child's expiry to the parent's and report success, handing
// back a capability that was already dead — a failure the caller met later,
// in another process, as an authentication error.
func TestChild_RefusesAParentThatIsOnlyAliveOnSkew(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	h.clock = time.Unix(owner.Context().GetExpiresAtUnix(), 0).Add(10 * time.Second)
	onSkew, err := h.verify(audience, owner.Encoded())
	require.NoError(t, err, "the parent is still accepted inside the skew window")

	_, _, err = h.authority.Child(context.Background(), onSkew, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           30 * time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "can open no further session")
}

func TestGrant_RefusesAParentThatIsOnlyAliveOnSkew(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")

	h.clock = time.Unix(agent.Context().GetExpiresAtUnix(), 0).Add(10 * time.Second)
	grant.NotAfter = h.clock.Add(time.Hour)
	onSkew, err := h.verify(audience, agent.Encoded())
	require.NoError(t, err)

	_, _, err = h.authority.Grant(context.Background(), onSkew, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "can open no further session")
}

// A minted capability must not share claim objects with the parent's verified
// view: mutating the child would otherwise rewrite what the parent is
// verified to hold, and a later exchange off that parent would attenuate
// against the rewritten authority and mint a signed widening.
func TestChild_DoesNotAliasTheParentsVerifiedClaims(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	before := proto.Clone(agent.Context()).(*basev0.WorkContextV1)

	_, child, err := h.authority.Child(context.Background(), agent, workcontext.ChildInput{
		PrincipalID:   "a-sub",
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/sub:1.0.0",
		DelegationID:  "d-2",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.NoError(t, err)

	require.NotSame(t, agent.Context().GetActorChain()[0], child.GetActorChain()[0])
	child.GetActorChain()[0].GrantedScopes = []*basev0.WorkScopeV1{scope("repo", []string{"read", "write"}, nil)}
	child.GetAuthorityScopes()[0].Actions = []string{"admin"}

	require.True(t, proto.Equal(before, agent.Context()),
		"the parent's verified claims are unchanged by anything done to the child")
}

// The caller's own slices must not be captured either: a caller that reuses
// its scope buffer would otherwise rewrite the minted capability's claims.
func TestStart_DoesNotCaptureTheCallersScopes(t *testing.T) {
	h := newHarness(t)
	scopes := []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})}

	_, wc, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID: installation,
		TenantID:       tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
		OrganizationID: organization, TaskID: taskID, Audience: audience,
		AuthorityScopes: scopes, TTL: time.Hour,
	})
	require.NoError(t, err)

	scopes[0].Actions = []string{"admin"}
	require.Equal(t, []string{"read"}, wc.GetAuthorityScopes()[0].GetActions())
}

// A revoked parent derives NOTHING. This test asserted the opposite until a
// second reviewer showed the opposite was an exploit.
//
// It used to read: "a child that inherited the parent's revision was born
// superseded whenever a bump landed between the parent's verification and the
// exchange, so the minter reads the current one" — and it passed, pinning the
// laundering as correct. The bump IS the revocation: Verify refuses the parent
// itself with ErrRevoked after it, so re-reading the revision at the mint let
// the tenant-wide lever be escaped by deriving once. Measured before the fix:
// the child carried revision 8 and verified while its parent was refused.
//
// The thing re-reading legitimately bought is kept — a child of a CURRENT
// parent carries the current revision, which the second case asserts.
func TestChild_RefusesAParentWhoseRevisionIsSuperseded(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)

	h.revision++ // the bump is the revocation

	// The parent itself is refused, which is what makes deriving from it wrong.
	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)

	_, _, err = h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "mint afresh")
}

// A grant cannot be used to launder the revision either: getting an approval
// must not be a way around a bump.
func TestGrant_RefusesAParentWhoseRevisionIsSuperseded(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-revision")

	h.revision++

	_, _, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

// A child of a CURRENT parent carries the issuer's current revision, so a
// concurrent bump is the ordinary race rather than a stale stamp.
func TestChild_CarriesTheIssuersCurrentRevisionWhenTheParentIsCurrent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	token, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.NoError(t, err)

	child := h.mustVerify(audience, token)
	require.Equal(t, h.revision, child.Context().GetAuthorizationRevision())
}

// A grant elevates one actor. Keeping that actor's id while changing anything
// else the derived identity reads — kind, agent identity, organization —
// hands the approved scope to a different principal.
func TestVerify_RejectsAGrantHopThatKeepsTheIdAndChangesTheIdentity(t *testing.T) {
	forgeries := map[string]func(*basev0.WorkActorV1){
		"another kind":         func(hop *basev0.WorkActorV1) { hop.PrincipalKind = "human" },
		"another agent":        func(hop *basev0.WorkActorV1) { hop.AgentId = proto.String("codefly.dev/other:9.9.9") },
		"another organization": func(hop *basev0.WorkActorV1) { hop.OrganizationId = proto.String("org-elsewhere") },
	}
	for name, forge := range forgeries {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, token, _ := h.elevated(t)
			forged := proto.Clone(h.mustVerify(mergeTool, token).Context()).(*basev0.WorkContextV1)
			forged.Nonce = "forged-" + name
			forge(forged.ActorChain[len(forged.ActorChain)-1])

			_, err := h.verify(mergeTool, h.resign(forged))
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "rather than elevating")
		})
	}
}

// The retention sweep is amortized, so an entry the sweep has not reached yet
// must still be refused on its own expiry rather than on collection.
func TestMemoryReplayStore_RefusesWithinRetentionAndForgetsAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	store := workcontext.NewMemoryReplayStore()
	store.Now = func() time.Time { return now }
	ctx := context.Background()

	require.NoError(t, store.Consume(ctx, "n-1", now.Add(time.Minute)))
	require.ErrorIs(t, store.Consume(ctx, "n-1", now.Add(time.Minute)), workcontext.ErrReplayed)

	// Past retention but before any sweep would have run on its own.
	now = now.Add(2 * time.Minute)
	require.NoError(t, store.Consume(ctx, "n-1", now.Add(time.Minute)),
		"a nonce past its retention is no longer a replay")
}

func TestStart_RefusesAnOwnerWithNoPrincipalKind(t *testing.T) {
	h := newHarness(t)
	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID: installation,
		TenantID:       tenant, OwnerPrincipalID: ownerID, TaskID: taskID,
		Audience: audience, TTL: time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "owner's principal kind")
}

// A capability minted before owner_principal_kind existed is still a valid
// capability. The schema must not retroactively reject it — that would
// invalidate every archived capability and every receipt embedding one.
func TestVerify_AcceptsACapabilityMintedBeforeOwnerPrincipalKindExisted(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	archived := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	archived.OwnerPrincipalKind = nil
	archived.Nonce = "archived"

	verified, err := h.verify(audience, h.resign(archived))
	require.NoError(t, err)
	require.Empty(t, verified.Context().GetOwnerPrincipalKind())
}

// Verify is the only constructor for Verified: its fields are unexported, so
// no package outside this one can assemble a value asserting that signature,
// window, audience, attenuation, grant and replay checks all passed. With the
// schema no longer able to require the fields an identity is derived from,
// that gate is the only one left, and a forgeable Verified would walk past it.
//
// This test documents the property; the compiler enforces it, and a change
// that exported the fields again would make the equivalent of
// `&workcontext.Verified{Context: doctored}` compile in policy and in any
// consumer.
func TestVerified_CarriesExactlyWhatVerifyPut(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)

	require.Equal(t, token, owner.Encoded())
	require.Equal(t, workcontext.Fingerprint(token), owner.SHA256())
	require.Equal(t, ownerID, owner.Context().GetOwnerPrincipalId())
}

// A misconfigured verification key must refuse the capability, not crash the
// process. ed25519.Verify panics on a key that is not PublicKeySize bytes, and
// the key is selected by the untrusted capability's own key id — so one short
// entry in Keys (a truncated hex decode, a half-finished rotation) would let
// anyone who learns that key id take the verifying service down on demand.
func TestVerify_RefusesAMisconfiguredKeyInsteadOfPanicking(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token := owner.Encoded()

	for name, key := range map[string]ed25519.PublicKey{
		"truncated": h.public[:16],
		"empty":     {},
		"oversized": append(append(ed25519.PublicKey{}, h.public...), 0),
	} {
		t.Run(name, func(t *testing.T) {
			verifier := h.verifier(audience)
			verifier.Keys = map[string]ed25519.PublicKey{keyID: key}
			_, err := verifier.Verify(context.Background(), token)
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "not 32")
		})
	}

	// The correctly sized key still verifies the same capability, so the guard
	// rejects only what would have panicked.
	verified, err := h.verify(audience, token)
	require.NoError(t, err)
	require.NotNil(t, verified)
}
