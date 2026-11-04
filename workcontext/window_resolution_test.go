package workcontext_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// A capability states WHOLE SECONDS, and the mint answers at that resolution.
//
// The minter's guards compare the clock at nanosecond precision — Grant's
// "leaves no usable window", derive's "parent session expired" — while the
// three instants reach the wire as t.Unix(). Nothing noticed while nothing
// read the issued-at: a mint whose expiry truncated into its own second wrote
// iat == nbf == exp, and a verifier that checked only the two ends accepted it
// for Skew past an expiry that had already passed. Requiring a sound issued-at
// made every verifier refuse that token, with "issued at T, after it expires
// at T", in whichever process presented it.
//
// So the mint reported success for a credential born dead — which is the exact
// failure derive's own comment says its refusal exists to prevent. The tests
// below are the two ways in: an approval window that closes inside the mint's
// wall-second, and a TTL shorter than a second.

// A grant minted in the last wall-second of its approval.
//
// This is the case no TTL floor reaches: the bound is grant.NotAfter, written
// by the approvals engine, and the requested TTL is a sound minute. The clock
// sits at 10:00:00.100 and the approval closes at 10:00:00.800, so the
// nanosecond guard sees 700ms of window and the wire has none.
//
// Reproduced for review r49/F1 against ab484b8c: the mint returned success
// with iat == nbf == exp == 1790416800, Verify refused it at the mint instant
// and again 200ms later while the approval was still open, and the same token
// verified and was consumed against the commit before.
func TestGrant_RefusesAWindowThatClosesInsideItsOwnMintSecond(t *testing.T) {
	h := newHarness(t)
	h.clock = h.clock.Add(100 * time.Millisecond)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	grant := h.approvedGrant("g-last-second")
	grant.NotAfter = h.clock.Add(700 * time.Millisecond)
	require.Equal(t, h.clock.Unix(), grant.NotAfter.Unix(),
		"the case is an approval closing inside the mint's own wall-second")

	_, _, err := h.authority.Grant(context.Background(), agent,
		workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "leaves no whole second after the mint instant",
		"the refusal names the resolution, not the clock")
}

// A TTL below the resolution is refused where the TTL's other bounds are.
//
// Deterministically, which is the point: before MinTTL, Start(TTL: 500ms) at
// 10:00:00.100 minted iat == exp and Start(TTL: 500ms) at 10:00:00.900 minted
// a one-second window. Neither is 500ms, the first is refused by every
// verifier, and which one a caller got depended on the sub-second phase of the
// authority's clock.
func TestMint_RefusesATTLTheWireCannotState(t *testing.T) {
	for name, phase := range map[string]time.Duration{
		"on the second":       0,
		"early in the second": 100 * time.Millisecond,
		"late in the second":  900 * time.Millisecond,
		"just under a second": 999 * time.Millisecond,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.clock = h.clock.Add(phase)

			_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
				InstallationID:     installation,
				TenantID:           tenant,
				OwnerPrincipalID:   ownerID,
				OwnerPrincipalKind: "human",
				OrganizationID:     organization,
				TaskID:             taskID,
				Audience:           audience,
				AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
				TTL:                500 * time.Millisecond,
			})
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "no authority mints below 1s")

			// The same TTL on a child hop, where the floor is the same bound.
			_, owner := h.ownerSession(audience)
			_, _, err = h.authority.Child(context.Background(), owner, workcontext.ChildInput{
				PrincipalID:   agentID,
				PrincipalKind: "agent",
				AgentID:       "fixture.test/agent:1.0.0",
				DelegationID:  "d-sub-second",
				GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
				Audience:      audience,
				TTL:           500 * time.Millisecond,
			})
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "no authority mints below 1s")
		})
	}
}

// THE invariant, swept across the second boundary: a mint either REFUSES, or
// produces a capability its own issuer's verifier accepts at the instant it
// was minted. There is no third outcome, and "success" was the third one.
//
// This is a property rather than a case because the failure was not a value
// anybody wrote. It was the gap between the resolution the mint decided at and
// the resolution it wrote, and that gap opens or closes depending on where in
// the second the clock happens to be and where in the second the bound happens
// to fall — so the sweep is over both. A test on a whole second is blind to it:
// TestEveryMintFillsTheIssuedAtWithItsOwnMintInstant is, which is why the mint
// stayed green through the change that made every verifier refuse these.
//
// The bound swept is an approval's NotAfter, because that is the one no TTL
// floor reaches: it comes from the approvals engine, which is product-level
// and owes this package no rounding.
func TestEveryMintThatSucceedsVerifiesAtTheInstantItWasMinted(t *testing.T) {
	phases := map[string]time.Duration{
		"on the second":        0,
		"early in the second":  1 * time.Millisecond,
		"mid second":           500 * time.Millisecond,
		"late in the second":   900 * time.Millisecond,
		"one nanosecond short": time.Second - time.Nanosecond,
	}
	// Offsets either side of, and exactly on, the boundary the truncation
	// turns on.
	offsets := []time.Duration{
		1 * time.Millisecond, 100 * time.Millisecond, 700 * time.Millisecond,
		999 * time.Millisecond, time.Second, 1100 * time.Millisecond,
		1500 * time.Millisecond, 2 * time.Second, 5 * time.Minute,
	}

	for name, phase := range phases {
		t.Run(name, func(t *testing.T) {
			var minted int
			for _, offset := range offsets {
				h := newHarness(t)
				h.clock = h.clock.Add(phase)
				_, owner := h.ownerSession(audience)
				_, agent := h.agentSession(owner, audience)

				grant := h.approvedGrant("g-sweep")
				grant.NotAfter = h.clock.Add(offset)

				token, claims, err := h.authority.Grant(context.Background(), agent,
					workcontext.GrantInput{Grant: grant, TTL: time.Minute})
				if err != nil {
					// A refusal is a sound answer, and the only other one.
					require.ErrorIsf(t, err, workcontext.ErrInvalid, "offset %s", offset)
					continue
				}
				minted++

				// The capability the mint reported success for must be one
				// the issuer's own verifier takes, at the mint instant.
				_, err = h.verify(mergeTool, token)
				require.NoErrorf(t, err, "Grant reported success at clock %s with an approval closing in %s, "+
					"so the capability it handed back must verify; it stated iat=%d nbf=%d exp=%d",
					h.clock.Format(time.RFC3339Nano), offset,
					claims.GetIssuedAtUnix(), claims.GetNotBeforeUnix(), claims.GetExpiresAtUnix())
				require.Lessf(t, claims.GetIssuedAtUnix(), claims.GetExpiresAtUnix(),
					"offset %s: a stated window holds a whole second", offset)
			}
			require.NotZero(t, minted, "the sweep has to mint something, or it proves nothing")
		})
	}
}

// Start and Child state a window with a whole second in it at every phase,
// down to the shortest TTL an authority mints.
func TestStartAndChildStateAWholeSecondAtEveryPhase(t *testing.T) {
	for name, phase := range map[string]time.Duration{
		"on the second":        0,
		"early in the second":  1 * time.Millisecond,
		"late in the second":   999 * time.Millisecond,
		"one nanosecond short": time.Second - time.Nanosecond,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.clock = h.clock.Add(phase)

			ownerToken, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
				InstallationID:     installation,
				TenantID:           tenant,
				OwnerPrincipalID:   ownerID,
				OwnerPrincipalKind: "human",
				OrganizationID:     organization,
				TaskID:             taskID,
				Audience:           audience,
				AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read", "write", "merge"}, nil)},
				TTL:                workcontext.MinTTL,
			})
			require.NoError(t, err, "MinTTL is the shortest window statable, not an unmintable one")
			owner := h.mustVerify(audience, ownerToken)
			require.Less(t, owner.Context().GetIssuedAtUnix(), owner.Context().GetExpiresAtUnix())

			childToken, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
				PrincipalID:   agentID,
				PrincipalKind: "agent",
				AgentID:       "fixture.test/agent:1.0.0",
				DelegationID:  "d-phase",
				GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
				Audience:      audience,
				TTL:           workcontext.MinTTL,
			})
			require.NoError(t, err)
			child := h.mustVerify(audience, childToken)
			require.Less(t, child.Context().GetIssuedAtUnix(), child.Context().GetExpiresAtUnix())
		})
	}
}

// The verifier is NOT the place this was fixed, and this is the assertion that
// keeps it that way.
//
// Accepting iat == exp would declare a zero-length window sound — a capability
// that expires at the instant it is issued, carried by anything willing to
// hand it on. The mint refuses to produce one; the verifier refuses to take
// one; neither rule is the other's fallback.
//
// And the issued-at rule is the ONLY one that refuses it at the mint instant:
// the expiry rule accepts a capability for Skew past its expiry, which is
// deliberate and is why `now - Skew` is what it compares. A zero-length window
// is inside that band for thirty seconds.
func TestVerify_StillRefusesAZeroLengthWindow(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	rewritten := proto.CloneOf(owner.Context())
	rewritten.ExpiresAtUnix = rewritten.GetIssuedAtUnix()
	rewritten.Nonce = "nonce-zero-length-window"

	_, err := h.verify(audience, h.resign(rewritten))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "after it expires",
		"inside the skew band the expiry rule says nothing; the issued-at rule is what refuses a window of no length")
}
