package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

const (
	issuer       = "https://authority.codefly.test"
	keyID        = "k-1"
	tenant       = "t-acme"
	ownerID      = "u-antoine"
	agentID      = "a-mind"
	taskID       = "task-42"
	audience     = "codefly.dev/github-bot:0.1.0"
	organization = "org-platform"
	installation = "inst-alpha"
	bindingID    = "binding:alpha:reconcile"
)

// harness holds the real signer and verifier every test in this package runs
// against: no fake crypto, no stubbed verification.
type harness struct {
	t         *testing.T
	authority *workcontext.Authority
	public    ed25519.PublicKey
	clock     time.Time
	revision  uint64
	grants    map[string]*workcontext.Grant
	replay    *workcontext.MemoryReplayStore
	seals     *workcontext.MemorySealSource
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	h := &harness{
		t:        t,
		public:   public,
		clock:    time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC),
		grants:   map[string]*workcontext.Grant{},
		revision: 7,
	}
	h.replay = workcontext.NewMemoryReplayStore()
	h.replay.Now = func() time.Time { return h.clock }
	h.seals = workcontext.NewMemorySealSource()
	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 3,
		BuildIncarnation:     11,
	}))
	require.NoError(t, h.seals.PutEpoch(ownerID, 2))
	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 1,
	}))
	// Every principal that appears as an actor needs a live epoch, because a
	// hop without one cannot be revoked and is refused.
	for _, principal := range []string{agentID, "a-sub", approver} {
		require.NoError(t, h.seals.PutEpoch(principal, 1))
	}
	h.authority = &workcontext.Authority{
		Issuer:    issuer,
		KeyID:     keyID,
		Key:       private,
		Revisions: h,
		Seals:     h.seals,
		Now:       func() time.Time { return h.clock },
	}
	return h
}

// verifier returns a verifier for one audience, sharing the harness clock,
// grant table and replay store.
func (h *harness) verifier(aud string) *workcontext.Verifier {
	return &workcontext.Verifier{
		Issuer:    issuer,
		Audience:  aud,
		Keys:      map[string]ed25519.PublicKey{keyID: h.public},
		Revisions: h,
		Replay:    h.replay,
		Grants:    h,
		Seals:     h.seals,
		Now:       func() time.Time { return h.clock },
	}
}

func (h *harness) AuthorizationRevision(context.Context, string) (uint64, error) {
	return h.revision, nil
}

func (h *harness) Grant(_ context.Context, id string) (*workcontext.Grant, error) {
	grant, known := h.grants[id]
	if !known {
		return nil, errUnknownGrant
	}
	return grant, nil
}

var errUnknownGrant = errors.New("no such grant")

func (h *harness) verify(aud, token string) (*workcontext.Verified, error) {
	return h.verifier(aud).Verify(context.Background(), token)
}

func (h *harness) mustVerify(aud, token string) *workcontext.Verified {
	h.t.Helper()
	verified, err := h.verify(aud, token)
	require.NoError(h.t, err)
	return verified
}

func scope(kind string, actions []string, ids []string) *basev0.WorkScopeV1 {
	return &basev0.WorkScopeV1{ResourceKind: kind, Actions: actions, ResourceIds: ids}
}

// ownerSession is step 1: the human starts a task holding read and write on
// every repo of the tenant.
func (h *harness) ownerSession(aud string) (string, *workcontext.Verified) {
	h.t.Helper()
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID:     installation,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		OrganizationID:     organization,
		TaskID:             taskID,
		Audience:           aud,
		AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read", "write"}, nil)},
		TTL:                time.Hour,
	})
	require.NoError(h.t, err)
	return token, h.mustVerify(aud, token)
}

// agentSession is step 2: the agent gets read on one repo and nothing else.
func (h *harness) agentSession(parent *workcontext.Verified, aud string) (string, *workcontext.Verified) {
	h.t.Helper()
	token, _, err := h.authority.Child(context.Background(), parent, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
		Audience:      aud,
		TTL:           30 * time.Minute,
	})
	require.NoError(h.t, err)
	return token, h.mustVerify(aud, token)
}

// resign signs an arbitrary claims message with the harness key. It is how a
// test presents a capability the minting API would refuse to produce, so the
// verifier is checked on its own and not merely through the minter.
func (h *harness) resign(wc *basev0.WorkContextV1) string {
	h.t.Helper()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
	require.NoError(h.t, err)
	signature := ed25519.Sign(h.authority.Key, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestStart_OwnerActsWithNoActorHop(t *testing.T) {
	h := newHarness(t)
	_, verified := h.ownerSession(audience)

	require.Empty(t, verified.Context().GetActorChain())
	require.Nil(t, verified.Actor())
	require.Equal(t, ownerID, verified.Context().GetOwnerPrincipalId())
	require.Equal(t, workcontext.Typ, verified.Context().GetTyp())
	require.Equal(t, workcontext.ReplayIdempotent, verified.Context().GetReplayPolicy())
	require.Len(t, verified.SHA256(), 64)
	require.Equal(t, []string{"read", "write"}, verified.EffectiveScopes()[0].GetActions())
}

func TestChild_NarrowsAuthorityAndKeepsTaskIdentity(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	require.Equal(t, taskID, agent.Context().GetTaskId())
	require.Equal(t, tenant, agent.Context().GetTenantId())
	require.Equal(t, ownerID, agent.Context().GetOwnerPrincipalId())
	require.Equal(t, owner.Context().GetSessionId(), agent.Context().GetParentSessionId())
	require.NotEqual(t, owner.Context().GetSessionId(), agent.Context().GetSessionId())
	require.Equal(t, agentID, agent.Actor().GetPrincipalId())
	require.Equal(t, []string{"read"}, agent.EffectiveScopes()[0].GetActions())
}

func TestChild_NeverExtendsExpiry(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	token, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           10 * time.Hour,
	})
	require.NoError(t, err)
	child := h.mustVerify(audience, token)

	require.Equal(t, owner.Context().GetExpiresAtUnix(), child.Context().GetExpiresAtUnix())
}

func TestChild_RejectsWidening(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	for name, widened := range map[string]*basev0.WorkScopeV1{
		"an action the parent does not hold":  scope("repo", []string{"write"}, []string{"codefly/core"}),
		"a resource the parent does not name": scope("repo", []string{"read"}, []string{"codefly/other"}),
		"every resource of the kind":          scope("repo", []string{"read"}, nil),
		"a kind the parent does not hold":     scope("secret", []string{"read"}, []string{"db"}),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := h.authority.Child(context.Background(), agent, workcontext.ChildInput{
				PrincipalID:   "a-sub",
				PrincipalKind: "agent",
				AgentID:       "codefly.dev/sub:1.0.0",
				DelegationID:  "d-2",
				GrantedScopes: []*basev0.WorkScopeV1{widened},
				Audience:      audience,
				TTL:           time.Minute,
			})
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "widens authority")
		})
	}
}

// A widening hop that never went through the minter must still be refused:
// attenuation is a property the verifier enforces, not a courtesy of minting.
// The widened hop here stays inside the owner's authority scopes and is still
// refused, because each hop is held against the hop before it.
func TestVerify_RejectsAWideningHopThatWasSignedAnyway(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	forged := proto.Clone(agent.Context()).(*basev0.WorkContextV1)
	forged.Nonce = "forged-nonce"
	forged.ActorChain = append(forged.ActorChain, &basev0.WorkActorV1{
		PrincipalId:   "a-sub",
		PrincipalKind: "agent",
		DelegationId:  "d-2",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"write"}, []string{"codefly/core"})},
	})

	_, err := h.verify(audience, h.resign(forged))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "widens authority")
}

func TestVerify_RejectsAnotherAudience(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token, _ := h.agentSession(owner, audience)

	_, err := h.verify("codefly.dev/other-bot:0.1.0", token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "presented to")
}

func TestVerify_RejectsATamperedPayload(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	tampered := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	tampered.OwnerPrincipalId = "u-someone-else"
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(tampered)
	require.NoError(t, err)
	_, signature, _ := strings.Cut(owner.Encoded(), ".")

	_, err = h.verify(audience, base64.RawURLEncoding.EncodeToString(payload)+"."+signature)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "signature does not verify")
}

func TestVerify_RejectsAnotherIssuersKey(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	other, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	verifier := h.verifier(audience)
	verifier.Keys = map[string]ed25519.PublicKey{keyID: other}

	_, err = verifier.Verify(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
}

func TestVerify_RejectsAnExpiredCapability(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	h.clock = h.clock.Add(2 * time.Hour)
	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "expired at")
}

func TestVerify_RejectsASupersededAuthorizationRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	h.revision++
	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

// Every source a Verifier holds is load-bearing, so a missing one is refused
// rather than read as "that check is off". The seal source is in the list for
// the same reason as the rest: a verifier without one could not tell a
// capability sealed to a superseded installation from a current one, and
// skipping the strongest check in the model would be the easiest thing to do
// by accident.
func TestVerify_RefusesWithoutAnyOneOfItsSources(t *testing.T) {
	for name, remove := range map[string]func(*workcontext.Verifier){
		"revisions": func(v *workcontext.Verifier) { v.Revisions = nil },
		"replay":    func(v *workcontext.Verifier) { v.Replay = nil },
		"grants":    func(v *workcontext.Verifier) { v.Grants = nil },
		"seals":     func(v *workcontext.Verifier) { v.Seals = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			token, _ := h.ownerSession(audience)

			verifier := h.verifier(audience)
			remove(verifier)
			_, err := verifier.Verify(context.Background(), token)
			require.ErrorContains(t, err, "missing a revision source, replay store, grant source or seal source")
		})
	}
}

// resignWithAnotherKey re-signs a token's claims with a key the verifier does
// not hold, keeping the payload byte-identical.
func (h *harness) resignWithAnotherKey(t *testing.T, token string) string {
	t.Helper()
	payload, _, found := strings.Cut(token, ".")
	require.True(t, found)
	claims, err := base64.RawURLEncoding.DecodeString(payload)
	require.NoError(t, err)
	_, other, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(other, claims))
}
