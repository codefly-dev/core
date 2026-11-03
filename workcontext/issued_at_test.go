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

// unsoundIssuedAt is the four ways a capability's issued-at is not a sound
// claim, each rewriting ONLY that field on a capability the minter produced —
// so the window checks pass and the refusal under test is the issued-at and
// not whichever other rule a second edit would have tripped.
//
// The sound session they are built from carries not_before == issued_at ==
// the harness clock and expires an hour later, which is what makes each case
// one variable.
func unsoundIssuedAt(clock time.Time) map[string]struct {
	issued  time.Time
	message string
} {
	return map[string]struct {
		issued  time.Time
		message string
	}{
		// Absent. Zero is the only spelling proto3 has for "the issuer stated
		// nothing" in an int64, so it is refused as absent rather than read as
		// the epoch — a capability minted in 1970 is not a thing to diagnose.
		"missing": {issued: time.Unix(0, 0), message: "no issued-at"},
		// Post-dated: minted before the window it opens.
		"before the window opens": {issued: clock.Add(-time.Hour), message: "before its window opens"},
		// Minted after it expired. Here the future rule would catch it too and
		// the expiry rule reports it because it runs first; the case that only
		// the expiry rule can refuse needs a window shorter than the skew band
		// and has a test of its own below.
		"after the expiry": {issued: clock.Add(time.Hour + time.Minute), message: "after it expires"},
		// In the future, past the skew tolerance.
		"in the future": {issued: clock.Add(time.Minute), message: "in the future"},
	}
}

// The verifier REQUIRES an issued-at, and holds it against the window.
//
// Before this it read one of the two instants in the capability and never the
// third: signature, issuer, audience, not-before, expiry, structure, revision,
// seal, grant and nonce were all checked, and `issued_at_unix` was a claim on
// the wire that no rule touched. Every token below passed verification.
func TestVerify_RefusesEveryUnsoundIssuedAt(t *testing.T) {
	for name, tc := range unsoundIssuedAt(newHarness(t).clock) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, owner := h.ownerSession(audience)

			rewritten := proto.CloneOf(owner.Context())
			rewritten.IssuedAtUnix = tc.issued.Unix()

			_, err := h.verify(audience, h.resign(rewritten))
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), tc.message)
		})
	}
}

// ONE body, reached by every entrypoint that reads a window: the callee's
// Verify, the verify-only Authenticate, the forwarding hop's Inspect and the
// issuer's own ReadBack all refuse the same token with the same message, byte
// for byte.
//
// Equal messages rather than equal outcomes, for the reason the kit-wide
// agreement tests give: two bodies can agree on accept/refuse for every case
// anyone thought to test and still differ on which rule fired. The issued-at
// rule lives in checkWindow, which all four call, so there is nothing here to
// drift — and this is what breaks the moment a second copy appears.
//
// Recheck is absent deliberately and not by omission: it takes a *Verified,
// and a capability with an unsound issued-at can no longer produce one.
func TestTheIssuedAtRuleIsOneBodyOnEveryEntrypoint(t *testing.T) {
	for name, tc := range unsoundIssuedAt(newHarness(t).clock) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, owner := h.ownerSession(audience)

			rewritten := proto.CloneOf(owner.Context())
			rewritten.IssuedAtUnix = tc.issued.Unix()
			token := h.resign(rewritten)

			_, verifyErr := h.verify(audience, token)
			_, authErr := h.authenticator(audience).Authenticate(context.Background(), token)
			_, inspectErr := h.inspector().Inspect(token, audience)
			_, readBackErr := h.readBack(audience, token)

			require.Error(t, verifyErr)
			for entrypoint, err := range map[string]error{
				"Authenticate": authErr,
				"Inspect":      inspectErr,
				"ReadBack":     readBackErr,
			} {
				require.ErrorIsf(t, err, workcontext.ErrInvalid, "%s", entrypoint)
				require.Equalf(t, verifyErr.Error(), err.Error(),
					"%s must reach Verify's refusal, not its own", entrypoint)
			}
		})
	}
}

// The expiry rule is NOT redundant with the future rule, and the way to show
// that is a window shorter than the skew band.
//
// A capability expiring in ten seconds, carrying an issued-at twenty seconds
// out, is inside `now + Skew` — so the future rule says nothing — while its
// own claims say it was minted after it expired. Delete the expiry rule and
// this token verifies.
func TestVerify_RefusesAnIssuedAtPastAnExpiryThatIsStillInsideTheSkew(t *testing.T) {
	h := newHarness(t)
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID:     installation,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		OrganizationID:     organization,
		TaskID:             taskID,
		Audience:           audience,
		AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		TTL:                10 * time.Second,
	})
	require.NoError(t, err)
	sound := h.mustVerify(audience, token)

	rewritten := proto.CloneOf(sound.Context())
	rewritten.Nonce = "nonce-issued-at-past-a-short-expiry"
	rewritten.IssuedAtUnix = h.clock.Add(20 * time.Second).Unix()
	require.Less(t, rewritten.GetIssuedAtUnix(), h.clock.Add(workcontext.DefaultSkew).Unix(),
		"the point of this case is an issued-at the future rule accepts")

	_, err = h.verify(audience, h.resign(rewritten))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "after it expires")
}

// Equality is the NORMAL case, so it is accepted: core's minter sets
// not_before and issued_at to the same instant, and a rule that refused the
// shape its own issuer produces would have been caught by nothing but the
// fixtures — which are minted, so they would have failed as a set rather than
// naming the rule.
func TestVerify_AcceptsAnIssuedAtEqualToNotBefore(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	require.Equal(t, owner.Context().GetNotBeforeUnix(), owner.Context().GetIssuedAtUnix(),
		"the minter sets the two to one instant, which the accepted case rests on")
}

// EVERY issuer in this repository fills the claim the verifier now requires.
//
// There is exactly one — workcontext.Authority — and it reaches the field down
// two paths: Start writes the whole message, and derive clones its parent
// before overwriting the window, so Child and Grant go through the second. A
// capability that verifies proves the field is filled, but not that it is
// filled with the mint instant: a derived capability inheriting its PARENT's
// issued-at would verify for as long as the parent's window stayed open, and
// claim to have been minted at a time it was not. So each form is asserted
// against the clock it was minted at, not merely against zero.
func TestEveryMintFillsTheIssuedAtWithItsOwnMintInstant(t *testing.T) {
	h := newHarness(t)
	started := h.clock

	_, owner := h.ownerSession(audience)

	// The derived forms are minted a minute later, so inheriting the parent's
	// issued-at is distinguishable from writing their own.
	h.clock = h.clock.Add(time.Minute)
	derived := h.clock
	_, child := h.agentSession(owner, audience)
	grantToken, _, err := h.authority.Grant(context.Background(), child,
		workcontext.GrantInput{Grant: h.approvedGrant("g-issued-at"), TTL: time.Minute})
	require.NoError(t, err)
	granted := h.mustVerify(mergeTool, grantToken)

	for name, tc := range map[string]struct {
		claims *basev0.WorkContextV1
		at     time.Time
	}{
		"Start": {claims: owner.Context(), at: started},
		"Child": {claims: child.Context(), at: derived},
		"Grant": {claims: granted.Context(), at: derived},
	} {
		t.Run(name, func(t *testing.T) {
			require.NotZerof(t, tc.claims.GetIssuedAtUnix(), "%s must fill the issued-at", name)
			require.Equalf(t, tc.at.Unix(), tc.claims.GetIssuedAtUnix(),
				"%s must state its OWN mint instant, not one it inherited", name)
		})
	}
}
