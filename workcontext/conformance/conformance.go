// Package conformance proves that a consumer verifies Work Contexts through
// core's implementation, by driving core's own fixtures against the consumer's
// verification entrypoint.
//
// # Why this package exists
//
// A wire contract has exactly one implementation, in the repository that owns
// the type. The Work Context is a core proto, so core's workcontext package is
// the only mint and the only verify, and nothing else may sign, verify or
// re-encode one.
//
// That rule cannot be enforced in core, because core cannot see who
// re-implements it. It is enforced here instead, in the consumer's own test
// suite: a consumer that calls Run with its verification entrypoint and passes
// is using core's path, and a consumer that has quietly grown a second
// implementation cannot pass. The decisive case is the foreign-encoding
// fixture, which must be refused BEFORE its signature is checked and with
// workcontext.ErrNotACoreToken — a second implementation refuses that token
// too, but as a signature failure, which is the misdiagnosis that made this
// rule necessary in the first place.
//
// # How a consumer uses it
//
//	func TestWorkContextConformance(t *testing.T) {
//	    verifier := conformance.Verifier()          // core's, configured for the kit
//	    conformance.Run(t, func(ctx context.Context, token string) error {
//	        _, err := myPackage.VerifyWorkContext(ctx, token)   // the consumer's own entrypoint
//	        return err
//	    })
//	    _ = verifier
//	}
//
// The consumer's entrypoint must be configured with the kit's issuer,
// audience, key, revision source, replay store, seal source and grant source.
// Settings returns exactly those, and Verifier assembles core's Verifier from
// them for a consumer whose entrypoint simply is core's.
//
// # Two modes, because there are two entrypoints
//
// Run is for a consumer whose entrypoint is a full verifier: it holds the
// issuer's grant records and its live authorization revision, and it must
// reach every fixture's declared outcome.
//
// RunAuthenticator is for a consumer whose entrypoint is verify-only —
// workcontext.Authenticator, or a wrapper around it. It is STRICTER, not more
// lenient: every fixture that does not need the issuer's own records must
// reach the same outcome with the same named reason, and the fixtures that do
// must be REFUSED with workcontext.ErrNeedsIssuer. That last assertion is what
// keeps a second entrypoint from becoming a second strength, because an
// entrypoint that accepted an approval it never checked accepts those
// fixtures and fails here. A consumer picks the mode that matches its
// entrypoint; running the other one fails rather than passing quietly.
//
// The replay store matters: one of the fixtures is single-use, and Run presents
// it twice to check that the second presentation is refused. So the entrypoint
// must hold one replay store across the whole Run, which is what a real
// verifier does anyway.
//
// # A third mode, for the hop that forwards
//
// RunInspector is for a consumer whose entrypoint is a FORWARDING HOP's —
// workcontext.Inspector, or a wrapper around it. A hop checks the signature,
// the window and the route and forwards; it holds no live state and consumes
// nothing. The mode is stricter in both directions: every fixture a hop can
// see must reach the full verifier's outcome with the same named reason, and
// every fixture refused only against live state (NeedsLiveState) must be
// ACCEPTED — forwarded for the callee to refuse — because an inspector that
// refused one would be claiming a check it cannot make. It presents the
// single-use fixture twice and requires both to inspect, then verifies it
// through core's verifier on the same replay store and requires THAT to
// succeed: the nonce is intact, so the hop consumed nothing. And it asks the
// entrypoint about a route the capability is not addressed to, and about no
// route at all, and requires both refused.
package conformance

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codefly-dev/core/workcontext"
)

// Verify is a consumer's verification entrypoint: it takes a presented token
// and returns the error verification produced, or nil.
//
// It deliberately returns only an error. What a consumer does with the verified
// claims is the consumer's business; what this package checks is that it
// reaches the same accept/refuse decision, with the same named reason, as
// core's verifier does.
type Verify func(ctx context.Context, token string) error

// Inspect is a consumer's forwarding-hop entrypoint: it takes a presented
// token and the route target the hop resolved for it, and returns the error
// inspection produced, or nil. It mirrors (*workcontext.Inspector).Inspect,
// which takes no context because it does no I/O; a wrapper that refreshes its
// trust root on demand supplies its own.
type Inspect func(token, audience string) error

// TestingT is the part of *testing.T this package uses. It is an interface so
// that importing this package does not pull the testing flag set into a
// consumer's binary.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Settings is everything a verification entrypoint must be configured with
// before Run means anything. A consumer that configures its verifier from
// anything else is testing its own configuration rather than the contract.
type Settings struct {
	// Issuer is the authority the verifier must pin.
	Issuer string
	// Audience is the service the verifier answers for.
	Audience string
	// Keys are the public keys by key id.
	//
	// The value type is []byte rather than ed25519.PublicKey so that holding
	// Settings does not oblige a consumer to import crypto/ed25519. That has
	// one consequence worth naming here, because a consumer hit it: a
	// Verifier's Keys field is map[string]ed25519.PublicKey, so this field
	// cannot be assigned to it directly. Use PublicKeys().
	Keys map[string][]byte
	// Revisions is the issuer's authorization revision source.
	Revisions workcontext.RevisionSource
	// Replay is a replay store. One instance must serve the whole Run.
	Replay workcontext.ReplayStore
	// Seals is the live sealed state every accepted fixture is sealed to.
	Seals workcontext.SealSource
	// Grants resolves the approval the grant fixture carries.
	Grants workcontext.GrantSource
	// Now is the clock the fixtures were minted against. A verifier checking
	// the windows against a different clock will refuse sound fixtures, so a
	// consumer that pins a clock must pin this one.
	Now func() time.Time

	// TrustTheConformanceFixtureKey is always true. **COPY THIS INTO YOUR
	// VERIFIER OR EVERY FIXTURE IS REFUSED** for carrying the kit's key.
	//
	// A consumer read that warning, built its verifier field by field from
	// these settings, omitted this one bool, and spent ten minutes on 35
	// failures about a key. RunWith now detects the case and says so by name,
	// because a bool that must be true is the easiest kind of field to miss
	// when copying a struct literal.
	//
	// It is here, rather than left for the consumer to discover, because the
	// first version of this was not: a verifier refuses the fixture key
	// unless told otherwise, and the recipe this kit documents stopped
	// working — a consumer doing exactly the right thing could not pass. The
	// field is in Settings so the recipe is complete.
	//
	// Copy it into a CONFORMANCE verifier and nowhere else. Its name is this
	// long so that copying it into a production one is visible in review.
	TrustTheConformanceFixtureKey bool
}

// New returns the settings for one conformance run, pinned to the given clock.
// Pass time.Now() unless the consumer's verifier is itself pinned.
func New(now time.Time) Settings {
	keys := map[string][]byte{}
	for id, key := range workcontext.FixtureKeys() {
		keys[id] = key
	}
	clock := func() time.Time { return now }
	// The replay store reads the SAME clock as the verifier. A store on wall
	// time would, at a historical test clock, judge the grant's retention
	// deadline already past — so the second presentation of a single-use
	// capability would succeed and the kit would fail a conforming consumer
	// for the kit's own reason.
	replay := workcontext.NewMemoryReplayStore()
	replay.Now = clock
	return Settings{
		TrustTheConformanceFixtureKey: true,
		Issuer:                        workcontext.FixtureIssuer,
		Audience:                      workcontext.FixtureAudience,
		Keys:                          keys,
		Revisions:                     workcontext.FixtureRevisions(),
		Replay:                        replay,
		Seals:                         workcontext.FixtureSeals(),
		Grants:                        workcontext.FixtureGrants(now),
		Now:                           clock,
	}
}

// PublicKeys is Keys in the type a Verifier and an Authenticator take.
//
// It exists because the obvious thing a consumer does with Settings is build
// its own exported verifier FIELD BY FIELD — which is the only way the kit
// says anything about that consumer, since passing Settings.Verifier() to Run
// drives core's verifier and proves nothing about the caller. The one field
// that could not be assigned across was the keys, so every consumer doing the
// right thing had to write this conversion itself. That is a gap in this kit,
// not a chore for each consumer.
//
// Verifier and Authenticator below go through it too, so the conversion is
// exercised by every test in this package rather than sitting on a path only
// consumers take.
func (s Settings) PublicKeys() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey, len(s.Keys))
	for id, key := range s.Keys {
		keys[id] = ed25519.PublicKey(key)
	}
	return keys
}

// Verifier assembles core's Verifier from the kit's settings. A consumer whose
// verification entrypoint is core's — which, under the one-implementation rule,
// is every consumer — passes this verifier's Verify to Run.
//
// A consumer proving something about ITS OWN exported type builds that type
// field by field from these settings instead; see PublicKeys.
func (s Settings) Verifier() *workcontext.Verifier {
	return &workcontext.Verifier{
		TrustTheConformanceFixtureKey: s.TrustTheConformanceFixtureKey,
		Issuer:                        s.Issuer,
		Audience:                      s.Audience,
		Keys:                          s.PublicKeys(),
		Revisions:                     s.Revisions,
		Replay:                        s.Replay,
		Grants:                        s.Grants,
		Seals:                         s.Seals,
		Now:                           s.Now,
	}
}

// Verifier (the zero-argument package-level one) IS DELETED.
//
// It was `New(time.Now()).Verifier()` and returned a verifier that TRUSTS THE
// CONFORMANCE FIXTURE KEY, whose private half anyone can derive from core's
// source. I kept it with an argument that reads badly now: that the package
// name at the call site makes it visible in review, and that deleting it would
// push consumers into assembling the same thing less carefully.
//
// Round six was right that this left M8 open. Moving the fixture seed off the
// verify path stopped every verifying binary from LINKING the key; it did
// nothing about the shortest possible call that TRUSTS it, which is the actual
// hazard and the one core's own README calls "the one mistaken call".
//
// What replaces it is not a weaker verifier but an explicit one:
// `conformance.New(now).Verifier()`, or passing Settings to Run. Both make the
// kit's involvement a thing the caller wrote down. That is one extra call for
// a conformance suite and no loss of capability, which is why this is a
// deletion rather than the build tag or separate module the README floats —
// those remain available if a reviewer wants the kit unreachable rather than
// merely un-convenient, and they cost every consumer's suite a change.

// Authenticator assembles core's verify-only entrypoint from the kit's
// settings, for a consumer whose entrypoint is that one. It is handed to
// RunAuthenticator, never to Run: the two modes require different outcomes for
// the fixtures the kit marks as needing the issuer's own records, and running
// the wrong one would either fail a conforming authenticator or pass a
// downgraded one.
//
// It takes the same revision source the full verifier does: the authorization
// revision is per tenant, so there is no single number to state.
func (s Settings) Authenticator() *workcontext.Authenticator {
	return &workcontext.Authenticator{
		TrustTheConformanceFixtureKey: s.TrustTheConformanceFixtureKey,
		Issuer:                        s.Issuer,
		Audience:                      s.Audience,
		Keys:                          s.PublicKeys(),
		Seals:                         s.Seals,
		Revisions:                     s.Revisions,
		Replay:                        s.Replay,
		Now:                           s.Now,
	}
}

// Inspector assembles core's forwarding-hop entrypoint from the kit's
// settings: the trust root, the clock and the fixture-key flag, and none of
// the four sources, because a hop holds none. It is handed to RunInspector,
// never to Run or RunAuthenticator: those modes require a single-use fixture
// consumed and the live-state fixtures refused, which is the callee's
// behaviour and the opposite of a hop's.
func (s Settings) Inspector() *workcontext.Inspector {
	return &workcontext.Inspector{
		TrustTheConformanceFixtureKey: s.TrustTheConformanceFixtureKey,
		Issuer:                        s.Issuer,
		Keys:                          s.PublicKeys(),
		Now:                           s.Now,
	}
}

// Run drives every fixture against verify and reports, per fixture, any
// outcome that differs from the contract. It uses a clock of its own and mints
// the fixtures fresh, so two consumers running it cannot interfere.
//
// A consumer calling Run must have configured its entrypoint from the same
// Settings; use RunWith when the consumer needs the settings in hand first.
func Run(t TestingT, verify Verify) {
	t.Helper()
	RunWith(t, New(time.Now()), verify)
}

// RunWith drives every fixture against verify using settings the caller
// already holds — for a consumer that had to build its verifier before it
// could hand over an entrypoint.
func RunWith(t TestingT, settings Settings, verify Verify) {
	t.Helper()
	if verify == nil {
		t.Fatalf("work context conformance: no verification entrypoint")
		return
	}
	fixtures, err := workcontext.Fixtures(settings.Now())
	if err != nil {
		t.Fatalf("work context conformance: the kit itself did not build: %v", err)
		return
	}
	if len(fixtures) == 0 {
		t.Fatalf("work context conformance: the kit is empty")
		return
	}
	ctx := context.Background()
	var accepted, rejected, fixtureKeyRefusals int
	for _, fixture := range fixtures {
		if fixture.Outcome == workcontext.OutcomeAccepted {
			accepted++
		} else {
			rejected++
		}
		err := verify(ctx, fixture.Token)
		// Counted only over the fixtures a conforming verifier ACCEPTS. The
		// shape and foreign-encoding fixtures are refused before a key is
		// ever looked up, so they say nothing either way.
		if fixture.Outcome == workcontext.OutcomeAccepted && err != nil &&
			strings.Contains(err.Error(), "conformance fixture key") {
			fixtureKeyRefusals++
		}
		checkFixture(t, ctx, fixture, verify, err)
	}
	// One diagnosis the kit can make for itself, because it can see both the
	// settings it was handed and the error every fixture came back with.
	//
	// A consumer read an explicit warning about TrustTheConformanceFixtureKey,
	// built its verifier field by field from these settings, omitted the bool,
	// and watched all 35 fixtures fail with the fixture-key refusal. It cost
	// ten minutes that this line costs nothing: a bool that must be true is
	// the easiest kind of field to miss when copying a struct literal, and the
	// kit is the only thing positioned to notice.
	if accepted > 0 && fixtureKeyRefusals == accepted {
		t.Fatalf("work context conformance: every fixture that should VERIFY was refused for carrying the conformance fixture key. " +
			"Did you copy TrustTheConformanceFixtureKey from the settings into your verifier? " +
			"A verifier refuses that key unless told otherwise, because its private half is derivable from core's source.")
		return
	}
	// A kit that drove no accepted fixture, or no refused one, would pass for a
	// verifier that answered the same way to everything.
	if accepted == 0 || rejected == 0 {
		t.Fatalf("work context conformance: the kit must drive both outcomes, got %d accepted and %d refused", accepted, rejected)
	}
	if err := checkForms(fixtures); err != nil {
		t.Fatalf("work context conformance: %v", err)
	}
}

// checkFixture holds one fixture's result against its declared contract. Both
// Run and RunAuthenticator call it, so the two modes cannot drift into judging
// the same fixture differently — the only thing RunAuthenticator decides for
// itself is which fixtures are deferred to the issuer, and it asserts those
// separately.
func checkFixture(t TestingT, ctx context.Context, fixture workcontext.Fixture, verify Verify, err error) {
	t.Helper()
	switch fixture.Outcome {
	case workcontext.OutcomeAccepted:
		if err != nil {
			t.Errorf("work context conformance: fixture %q (%s) must verify and did not: %v\n  it is: %s",
				fixture.Name, fixture.Form, err, fixture.Reason)
			return
		}
		if fixture.SingleUse {
			// A single-use capability is consumed by verifying it, so
			// presenting it again must be refused. A verifier without a
			// durable replay store passes everything above and fails here,
			// which is the point: single-use is a property of the verifier,
			// not of the token.
			if second := verify(ctx, fixture.Token); !errors.Is(second, workcontext.ErrReplayed) {
				t.Errorf("work context conformance: fixture %q is single-use; a second presentation must be refused with ErrReplayed, got %v",
					fixture.Name, second)
			}
		}
	case workcontext.OutcomeRejected:
		checkRefusal(t, fixture, err)
	default:
		t.Errorf("work context conformance: fixture %q declares outcome %q, which is neither accepted nor rejected",
			fixture.Name, fixture.Outcome)
	}
}

// checkRefusal holds a refusal against the fixture's declared sentinel and
// message. Every mode judges a refusal through it, so the three cannot drift
// into accepting different reasons for the same fixture.
func checkRefusal(t TestingT, fixture workcontext.Fixture, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("work context conformance: fixture %q (%s) must be refused and was accepted\n  it is: %s",
			fixture.Name, fixture.Form, fixture.Reason)
		return
	}
	// The named reason is part of the contract. "Refused" and "refused for
	// the stated reason" are different guarantees, and the whole reason this
	// kit exists is an implementation that refused the right token with the
	// wrong error.
	if fixture.Err != nil && !errors.Is(err, fixture.Err) {
		t.Errorf("work context conformance: fixture %q (%s) must be refused with %v, got %v\n  it is: %s",
			fixture.Name, fixture.Form, fixture.Err, err, fixture.Reason)
		return
	}
	// Several fixtures share a sentinel, so where the sentinel is an umbrella
	// the fixture also names the refusal it must be. The tampered payload is
	// the case that matters: it exists to be the one SIGNATURE failure, and
	// asserting only ErrInvalid would let it pass for any unrelated
	// invalidity.
	if fixture.Message != "" && !strings.Contains(err.Error(), fixture.Message) {
		t.Errorf("work context conformance: fixture %q (%s) must be refused with a message containing %q, got %v\n  it is: %s",
			fixture.Name, fixture.Form, fixture.Message, err, fixture.Reason)
	}
}

// RunAuthenticator drives every fixture against a VERIFY-ONLY entrypoint —
// core's Authenticator, or a consumer's wrapper around it — and requires it to
// reach the full verifier's outcome for every fixture except the ones the kit
// marks as needing the issuer's own records, which it must REFUSE with
// workcontext.ErrNeedsIssuer.
//
// # What this mode is for
//
// Shipping a second entrypoint invites a second strength: a consumer reaches
// for the weaker one where the stronger was needed, nothing fails, and every
// rule downstream now rests on a question nobody asked. A kit that simply
// passed a verify-only verifier unchanged would have been certifying exactly
// that.
//
// So this mode is stricter than Run, not more lenient. For every fixture that
// does not need the issuer's records it requires the SAME outcome and the SAME
// named reason, so a verify-only entrypoint cannot be weaker anywhere. And for
// the fixtures that do, it requires a refusal naming ErrNeedsIssuer — which is
// the one assertion a downgraded authenticator fails, because a verifier that
// accepted an approval it never checked ACCEPTS that fixture.
//
// A consumer whose entrypoint is a full verifier must not call this: it would
// fail on the issuer-backed fixtures, correctly, since it accepts them.
func RunAuthenticator(t TestingT, settings Settings, authenticate Verify) {
	t.Helper()
	if authenticate == nil {
		t.Fatalf("work context conformance: no verification entrypoint")
		return
	}
	fixtures, err := workcontext.Fixtures(settings.Now())
	if err != nil {
		t.Fatalf("work context conformance: the kit itself did not build: %v", err)
		return
	}
	if len(fixtures) == 0 {
		t.Fatalf("work context conformance: the kit is empty")
		return
	}
	ctx := context.Background()
	var matched, deferred int
	for _, fixture := range fixtures {
		err := authenticate(ctx, fixture.Token)
		if fixture.NeedsIssuer {
			deferred++
			// The decisive assertion. Accepting here is what a downgraded
			// authenticator does, and refusing for any other reason would
			// mean the capability was rejected by accident rather than
			// deferred to the party that can answer.
			if !errors.Is(err, workcontext.ErrNeedsIssuer) {
				t.Errorf("work context conformance: fixture %q (%s) needs the issuer's own records, so a verify-only "+
					"entrypoint must refuse it with ErrNeedsIssuer, got %v\n  it is: %s",
					fixture.Name, fixture.Form, err, fixture.Reason)
			}
			continue
		}
		matched++
		checkFixture(t, ctx, fixture, authenticate, err)
	}
	// A kit run that exercised no issuer-backed fixture proved nothing about
	// the downgrade, and one that exercised only those proved nothing about
	// the rest.
	if deferred == 0 {
		t.Fatalf("work context conformance: no fixture needs the issuer's own records, so this mode certifies nothing")
	}
	if matched == 0 {
		t.Fatalf("work context conformance: every fixture needs the issuer's own records, so nothing was held to the full verifier's outcome")
	}
	if err := checkForms(fixtures); err != nil {
		t.Fatalf("work context conformance: %v", err)
	}
}

// RunInspector drives every fixture against a FORWARDING HOP's entrypoint —
// core's Inspector, or a consumer's wrapper around it — with the kit's
// audience as the route target, and requires:
//
//   - every fixture a hop CAN see reaches the full verifier's outcome with the
//     same named reason: the encoding, the shape, the schema, the lifetime
//     bound, the signature, the issuer, the audience, the window, the chain;
//   - every fixture refused ONLY against live state (NeedsLiveState) is
//     ACCEPTED — forwarded, for the callee holding that state to refuse. An
//     inspector that refused one would be claiming a check it cannot make,
//     which is the pretence this mode exists to catch;
//   - the single-use fixture inspects on BOTH of two presentations, and core's
//     verifier built from the same settings — the same replay store — then
//     accepts it once and refuses it the second time. The first acceptance is
//     what proves the hop consumed nothing; the second refusal is what proves
//     the first was not vacuous;
//   - a sound capability presented for a route it is not addressed to is
//     refused, and one presented for no route at all is refused.
//
// A consumer whose entrypoint is a callee's — Verify or Authenticate — must
// not call this. Both fail it, correctly: Verify consumes the single-use
// fixture and refuses the live-state ones, and Authenticate additionally
// refuses the grant fixture, which a hop forwards.
//
// The nonce oracle consumes the single-use fixture through the settings'
// replay store — the one the consumer was told to use. Every run mints its
// fixtures afresh, nonces included, so a store that served another run cannot
// interfere; what the oracle sees is this run's hop, and nothing else.
func RunInspector(t TestingT, settings Settings, inspect Inspect) {
	t.Helper()
	if inspect == nil {
		t.Fatalf("work context conformance: no inspection entrypoint")
		return
	}
	fixtures, err := workcontext.Fixtures(settings.Now())
	if err != nil {
		t.Fatalf("work context conformance: the kit itself did not build: %v", err)
		return
	}
	if len(fixtures) == 0 {
		t.Fatalf("work context conformance: the kit is empty")
		return
	}
	if settings.Audience == "" {
		t.Fatalf("work context conformance: the settings name no audience, so there is no route to inspect for")
		return
	}
	var forwarded, matched, singleUse int
	var sound workcontext.Fixture
	var haveSound bool
	for _, fixture := range fixtures {
		err := inspect(fixture.Token, settings.Audience)
		if fixture.NeedsLiveState {
			forwarded++
			if err != nil {
				t.Errorf("work context conformance: fixture %q (%s) is refused only against the issuer's live state, "+
					"which a forwarding hop does not hold, so Inspect must FORWARD it and leave the refusal to the callee; got %v\n  it is: %s",
					fixture.Name, fixture.Form, err, fixture.Reason)
			}
			continue
		}
		matched++
		switch fixture.Outcome {
		case workcontext.OutcomeAccepted:
			if fixture.SingleUse {
				singleUse++
			}
			if err != nil {
				t.Errorf("work context conformance: fixture %q (%s) must inspect and did not: %v\n  it is: %s",
					fixture.Name, fixture.Form, err, fixture.Reason)
				continue
			}
			if !haveSound && !fixture.SingleUse {
				sound, haveSound = fixture, true
			}
			if fixture.SingleUse {
				// A hop consumes nothing, so the second presentation inspects
				// too. ErrReplayed here means the entrypoint burned the nonce,
				// which is the callee's to burn.
				if second := inspect(fixture.Token, settings.Audience); second != nil {
					t.Errorf("work context conformance: fixture %q is single-use and a forwarding hop consumes nothing, "+
						"so a second presentation must inspect too; got %v", fixture.Name, second)
				}
			}
		case workcontext.OutcomeRejected:
			checkRefusal(t, fixture, err)
		default:
			t.Errorf("work context conformance: fixture %q declares outcome %q, which is neither accepted nor rejected",
				fixture.Name, fixture.Outcome)
		}
	}
	// THE NONCE IS INTACT. Core's verifier, built from the same settings and
	// so consuming through the same replay store the consumer was handed,
	// must still accept every single-use fixture the hop inspected — and
	// refuse it the second time, or the first acceptance proved nothing.
	verifier := settings.Verifier()
	ctx := context.Background()
	for _, fixture := range fixtures {
		if !fixture.SingleUse || fixture.Outcome != workcontext.OutcomeAccepted {
			continue
		}
		if _, err := verifier.Verify(ctx, fixture.Token); err != nil {
			t.Errorf("work context conformance: after inspection the callee's verifier must still accept the single-use "+
				"fixture %q — its nonce must be intact, so the hop must have consumed nothing; got %v", fixture.Name, err)
			continue
		}
		if _, err := verifier.Verify(ctx, fixture.Token); !errors.Is(err, workcontext.ErrReplayed) {
			t.Errorf("work context conformance: the replay store handed to this run must consume fixture %q on the "+
				"callee's verification, or the acceptance before it proved nothing; second verification got %v",
				fixture.Name, err)
		}
	}
	// THE ROUTE IS THE HOP'S. A sound capability asked about for another
	// route is refused with the audience refusal, and no route at all is
	// refused rather than read as a wildcard.
	if !haveSound {
		t.Fatalf("work context conformance: no accepted, replayable fixture to present for another route")
		return
	}
	if err := inspect(sound.Token, settings.Audience+"/another-route"); !errors.Is(err, workcontext.ErrInvalid) ||
		!strings.Contains(err.Error(), "presented to") {
		t.Errorf("work context conformance: fixture %q presented for a route it is not addressed to must be refused "+
			"with ErrInvalid naming the audience; got %v", sound.Name, err)
	}
	if err := inspect(sound.Token, ""); err == nil || !strings.Contains(err.Error(), "route target") {
		t.Errorf("work context conformance: fixture %q presented for NO route must be refused naming the missing route "+
			"target — an empty expectation is not a wildcard; got %v", sound.Name, err)
	}
	// A run that forwarded nothing proved nothing about the pretence, one
	// that matched nothing held nothing to the verifier, and one with no
	// single-use fixture proved nothing about consumption.
	if forwarded == 0 {
		t.Fatalf("work context conformance: no fixture needs live state, so this mode certifies nothing about forwarding")
	}
	if matched == 0 {
		t.Fatalf("work context conformance: every fixture needs live state, so nothing was held to the full verifier's outcome")
	}
	if singleUse == 0 {
		t.Fatalf("work context conformance: no accepted fixture is single-use, so nothing proved the hop consumes nothing")
	}
	if err := checkForms(fixtures); err != nil {
		t.Fatalf("work context conformance: %v", err)
	}
}

// checkForms fails when the kit stops covering a token form, so adding a form
// to the minter without adding a fixture for it cannot pass unnoticed.
func checkForms(fixtures []workcontext.Fixture) error {
	covered := map[workcontext.Form]bool{}
	for _, fixture := range fixtures {
		covered[fixture.Form] = true
	}
	for _, form := range []workcontext.Form{
		workcontext.FormSession,
		workcontext.FormOperation,
		workcontext.FormDelegated,
		workcontext.FormDelegatedOperation,
		workcontext.FormGrant,
		workcontext.FormForeign,
	} {
		if !covered[form] {
			return fmt.Errorf("the kit covers no %q token", form)
		}
	}
	return nil
}
