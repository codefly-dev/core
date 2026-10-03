package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/workcontext"
	"github.com/codefly-dev/core/workcontext/conformance"
)

// Core's own verifier passes its own kit. If this fails, the kit is wrong
// rather than the consumer.
func TestCoresVerifierPassesTheKit(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()
	conformance.RunWith(t, settings, func(ctx context.Context, token string) error {
		_, err := verifier.Verify(ctx, token)
		return err
	})
}

// The convenience path a consumer actually writes.
func TestRunWiresItsOwnSettings(t *testing.T) {
	verifier := conformance.Verifier()
	conformance.Run(t, func(ctx context.Context, token string) error {
		_, err := verifier.Verify(ctx, token)
		return err
	})
}

// recorder captures what Run reports, so the tests below can assert that the
// kit FAILS a non-conforming verifier. A conformance helper that cannot fail
// is a helper that proves nothing.
type recorder struct {
	errors []string
	fatal  []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, strings.TrimSpace(sprintf(format, args...)))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = append(r.fatal, strings.TrimSpace(sprintf(format, args...)))
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// The one fixture that decides whether a consumer is using core's
// implementation. A verifier that checks the signature before noticing the
// encoding refuses the look-alike too — with the wrong error — and that is
// exactly what the kit must catch, because the wrong error is what sent people
// looking at key rotation.
func TestTheKitFailsAVerifierThatMisdiagnosesTheLookAlike(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()

	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		err := verifyWith(ctx, verifier, token)
		// Stand in for a second implementation: it refuses the foreign token,
		// but reports a signature failure for it.
		if errors.Is(err, workcontext.ErrNotACoreToken) {
			return errors.New("work context: invalid: signature does not verify under key \"conformance-1\"")
		}
		return err
	})

	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "foreign-encoding")
	require.Contains(t, reported.errors[0], "not a core token")
}

// A verifier that accepts the look-alike outright fails too, and is the worse
// of the two.
func TestTheKitFailsAVerifierThatAcceptsTheLookAlike(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()

	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		err := verifyWith(ctx, verifier, token)
		if errors.Is(err, workcontext.ErrNotACoreToken) {
			return nil
		}
		return err
	})

	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "must be refused and was accepted")
}

// A verifier with no replay store passes every other fixture and fails the
// single-use one, because single-use is a property of the verifier rather than
// of the token.
func TestTheKitFailsAVerifierThatCannotConsumeASingleUseCapability(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()
	// A store that forgets: consumption records nothing, so a replay is never
	// detected. This is a real shape — an in-process store in a deployment
	// that verifies from two processes behaves this way across them.
	verifier.Replay = forgetfulReplay{}

	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		return verifyWith(ctx, verifier, token)
	})

	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "single-use")
	require.Contains(t, reported.errors[0], "ErrReplayed")
}

type forgetfulReplay struct{}

func (forgetfulReplay) Consume(context.Context, string, time.Time) error { return nil }

// A verifier that refuses everything fails loudly rather than passing by
// refusing the negatives for free.
func TestTheKitFailsAVerifierThatRefusesEverything(t *testing.T) {
	reported := &recorder{}
	conformance.Run(reported, func(context.Context, string) error {
		return errors.New("no")
	})
	require.NotEmpty(t, reported.errors)
}

// And one that accepts everything.
func TestTheKitFailsAVerifierThatAcceptsEverything(t *testing.T) {
	reported := &recorder{}
	conformance.Run(reported, func(context.Context, string) error { return nil })
	require.NotEmpty(t, reported.errors)
}

func TestRunNeedsAnEntrypoint(t *testing.T) {
	reported := &recorder{}
	conformance.Run(reported, nil)
	require.Len(t, reported.fatal, 1)
	require.Contains(t, reported.fatal[0], "no verification entrypoint")
}

// Settings must be what the fixtures were minted against, stated as values a
// consumer can read rather than as a comment telling it to guess.
func TestSettingsNameEverythingAVerifierNeeds(t *testing.T) {
	settings := conformance.New(time.Now())
	require.Equal(t, workcontext.FixtureIssuer, settings.Issuer)
	require.Equal(t, workcontext.FixtureAudience, settings.Audience)
	require.Contains(t, settings.Keys, workcontext.FixtureKeyID)
	require.NotNil(t, settings.Revisions)
	require.NotNil(t, settings.Replay)
	require.NotNil(t, settings.Seals)
	require.NotNil(t, settings.Grants)
	require.NotNil(t, settings.Now)
}

func verifyWith(ctx context.Context, verifier *workcontext.Verifier, token string) error {
	_, err := verifier.Verify(ctx, token)
	return err
}

// A historical clock must work. The kit pins the verifier's clock, and the
// replay store has to read the same one: on wall time it would judge the
// grant's retention deadline already past, so the single-use fixture's second
// presentation would succeed and the kit would fail a conforming consumer for
// its own reason rather than the consumer's.
func TestTheKitWorksAtAHistoricalInstant(t *testing.T) {
	settings := conformance.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	verifier := settings.Verifier()
	conformance.RunWith(t, settings, func(ctx context.Context, token string) error {
		_, err := verifier.Verify(ctx, token)
		return err
	})
}

// And a long way in the past, so the margin is not an accident of being near
// now.
func TestTheKitWorksAtADistantInstant(t *testing.T) {
	settings := conformance.New(time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC))
	verifier := settings.Verifier()
	conformance.RunWith(t, settings, func(ctx context.Context, token string) error {
		_, err := verifier.Verify(ctx, token)
		return err
	})
}

// The Message half of the contract is checked, not decoration. The tampered
// payload exists to be the ONE signature failure in the kit, and ErrInvalid is
// the umbrella for several unrelated refusals — so a verifier that refused it
// for the wrong reason must fail.
func TestTheKitFailsAVerifierThatRefusesTheTamperedPayloadForTheWrongReason(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()

	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		err := verifyWith(ctx, verifier, token)
		if err != nil && strings.Contains(err.Error(), "signature does not verify") {
			// Same sentinel, different reason: exactly the substitution the
			// Message field exists to catch.
			return fmt.Errorf("%w: audience mismatch", workcontext.ErrInvalid)
		}
		return err
	})

	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "tampered-payload")
	require.Contains(t, reported.errors[0], "signature does not verify")
}

// Every fixture that names a Message is reached by core's own verifier with
// that message, so the kit cannot ship a Message no implementation produces.
func TestEveryFixtureMessageIsProducedByCoresVerifier(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()
	fixtures, err := workcontext.Fixtures(settings.Now())
	require.NoError(t, err)

	var named int
	for _, fixture := range fixtures {
		if fixture.Message == "" {
			continue
		}
		named++
		_, err := verifier.Verify(context.Background(), fixture.Token)
		require.Errorf(t, err, "%s", fixture.Name)
		require.Containsf(t, err.Error(), fixture.Message, "%s", fixture.Name)
	}
	require.NotZero(t, named, "the kit must name at least one refusal message")
}

// Core's verify-only entrypoint passes the kit's authenticator mode.
func TestCoresAuthenticatorPassesTheAuthenticatorMode(t *testing.T) {
	settings := conformance.New(time.Now())
	authenticator := settings.Authenticator()
	conformance.RunAuthenticator(t, settings, func(ctx context.Context, token string) error {
		_, err := authenticator.Authenticate(ctx, token)
		return err
	})
}

// The assertion the whole mode exists for: a verify-only entrypoint that let
// an unchecked approval through FAILS, rather than passing the same kit a
// full verifier passes.
//
// This is the downgrade in its most plausible form — a GrantSource that
// answers whatever the capability claimed, which makes the grant hop
// self-authorizing and looks, at the call site, exactly like a verifier that
// checked something.
func TestTheAuthenticatorModeFailsAnEntrypointThatAcceptsAnUncheckedApproval(t *testing.T) {
	settings := conformance.New(time.Now())
	// The full verifier, which accepts the grant fixture because it holds the
	// issuer's record of the approval. Standing in for an Authenticator that
	// had been given a permissive grant source instead of refusing.
	permissive := settings.Verifier()

	reported := &recorder{}
	conformance.RunAuthenticator(reported, settings, func(ctx context.Context, token string) error {
		_, err := permissive.Verify(ctx, token)
		return err
	})

	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "grant")
	require.Contains(t, reported.errors[0], "must refuse it with ErrNeedsIssuer")
}

// And an entrypoint that is weaker anywhere ELSE fails the mode too: the
// fixtures that do not need the issuer's records are held to the full
// verifier's outcome, not to a relaxed one.
func TestTheAuthenticatorModeIsNotLenientOnTheRestOfTheKit(t *testing.T) {
	settings := conformance.New(time.Now())
	authenticator := settings.Authenticator()

	reported := &recorder{}
	conformance.RunAuthenticator(reported, settings, func(ctx context.Context, token string) error {
		err := authenticateWith(ctx, authenticator, token)
		// Stand in for an entrypoint that skipped the seal comparison: it
		// accepts a capability sealed to a superseded installation.
		if errors.Is(err, workcontext.ErrRevoked) {
			return nil
		}
		return err
	})

	require.Empty(t, reported.fatal)
	require.NotEmpty(t, reported.errors)
	for _, reportedError := range reported.errors {
		require.Contains(t, reportedError, "must be refused and was accepted")
	}
}

// The mode needs an entrypoint, like Run does.
func TestTheAuthenticatorModeNeedsAnEntrypoint(t *testing.T) {
	reported := &recorder{}
	conformance.RunAuthenticator(reported, conformance.New(time.Now()), nil)
	require.Len(t, reported.fatal, 1)
	require.Contains(t, reported.fatal[0], "no verification entrypoint")
}

func authenticateWith(ctx context.Context, authenticator *workcontext.Authenticator, token string) error {
	_, err := authenticator.Authenticate(ctx, token)
	return err
}

// sdkVerifier stands in for a consumer re-exporting core's Verifier as a true
// alias — which, under the one-implementation rule, is what every consumer's
// type resolves to.
type sdkVerifier = workcontext.Verifier

// The way a consumer proves something about ITS OWN exported type: build that
// type field by field from the kit's settings and run the kit against it.
//
// This test exists because a consumer's reviewer was right to object that
// passing Settings.Verifier() to RunWith drives CORE's verifier and says
// nothing about the caller. The field-by-field build is the answer, and the
// keys were the one field that would not assign across — so it is pinned here,
// from outside, in the shape a consumer actually writes.
func TestAConsumerCanBuildItsOwnExportedVerifierFromTheSettings(t *testing.T) {
	settings := conformance.New(time.Now())
	mine := &sdkVerifier{
		// A consumer must copy this, and the kit's Settings carries it so the
		// recipe is complete. Only a conformance verifier sets it.
		TrustTheConformanceFixtureKey: settings.TrustTheConformanceFixtureKey,
		Issuer:                        settings.Issuer,
		Audience:                      settings.Audience,
		Keys:                          settings.PublicKeys(),
		Revisions:                     settings.Revisions,
		Replay:                        settings.Replay,
		Grants:                        settings.Grants,
		Seals:                         settings.Seals,
		Now:                           settings.Now,
	}
	conformance.RunWith(t, settings, func(ctx context.Context, token string) error {
		_, err := mine.Verify(ctx, token)
		return err
	})
}

// PublicKeys must carry the same key material Keys holds, or a consumer
// building from it would get a verifier that refuses every fixture for a
// signature reason and would go looking at key rotation — the exact
// misdiagnosis this package exists because of.
func TestPublicKeysCarriesTheSameMaterialAsKeys(t *testing.T) {
	settings := conformance.New(time.Now())
	converted := settings.PublicKeys()
	require.Len(t, converted, len(settings.Keys))
	for id, raw := range settings.Keys {
		require.Equal(t, []byte(raw), []byte(converted[id]), "key %q", id)
	}
	public, _ := workcontext.FixtureKeyPair()
	require.Equal(t, []byte(public), []byte(converted[workcontext.FixtureKeyID]))
}

// A verifier that never checks expiry, or not-before, or the issuer, or the
// authorization revision, must FAIL the kit.
//
// Each of these passed the kit before the fixtures for them existed, which
// made the kit's claim to cover "every way one is refused" false. A
// conformance kit that a broken verifier passes is the failure it exists to
// prevent, so each omission is asserted to be caught.
func TestTheKitFailsAVerifierThatSkipsACheck(t *testing.T) {
	for name, skip := range map[string]func(error) error{
		"expiry":                func(err error) error { return nilIfContains(err, "expired at") },
		"not-before":            func(err error) error { return nilIfContains(err, "not valid before") },
		"the issuer":            func(err error) error { return nilIfContains(err, "issued by") },
		"the revision":          func(err error) error { return nilIfContains(err, "issuer is at") },
		"a revoked binding":     func(err error) error { return nilIfContains(err, "is revoked") },
		"a binding incarnation": func(err error) error { return nilIfContains(err, "incarnation") },
	} {
		t.Run(name, func(t *testing.T) {
			settings := conformance.New(time.Now())
			verifier := settings.Verifier()
			reported := &recorder{}
			conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
				return skip(verifyWith(ctx, verifier, token))
			})
			require.Empty(t, reported.fatal)
			require.NotEmpty(t, reported.errors,
				"a verifier that does not check %s must fail the kit", name)
			for _, reportedError := range reported.errors {
				require.Contains(t, reportedError, "must be refused and was accepted")
			}
		})
	}
}

// nilIfContains stands in for a verifier that never performs one check: it
// swallows exactly the refusal that check produces and reports everything
// else faithfully.
func nilIfContains(err error, message string) error {
	if err != nil && strings.Contains(err.Error(), message) {
		return nil
	}
	return err
}

// A verifier that accepts a foreign-minted token — one carrying a field this
// Core does not know — fails the kit. This is the one-implementation rule
// enforced at the wire rather than asserted in prose.
func TestTheKitFailsAVerifierThatAcceptsAForeignMintedToken(t *testing.T) {
	settings := conformance.New(time.Now())
	verifier := settings.Verifier()
	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		err := verifyWith(ctx, verifier, token)
		if err != nil && strings.Contains(err.Error(), "unknown field") {
			return nil
		}
		return err
	})
	require.Empty(t, reported.fatal)
	require.Len(t, reported.errors, 1)
	require.Contains(t, reported.errors[0], "unknown-field")
	require.Contains(t, reported.errors[0], "must be refused and was accepted")
}

// The kit diagnoses the one mistake its own recipe invites.
//
// A consumer read an explicit warning about TrustTheConformanceFixtureKey,
// built its verifier field by field from Settings — which is the construction
// the kit asks for, and the only one that says anything about the consumer —
// omitted the bool, and watched every fixture fail with an error about a key.
// The kit can see both the settings it was handed and the error every fixture
// came back with, so it is the only thing positioned to say what happened.
func TestTheKitNamesTheMissingFixtureKeyFlag(t *testing.T) {
	settings := conformance.New(time.Now())
	// Exactly what a consumer copying the recipe and missing one bool builds.
	forgot := &workcontext.Verifier{
		Issuer:    settings.Issuer,
		Audience:  settings.Audience,
		Keys:      settings.PublicKeys(),
		Revisions: settings.Revisions,
		Replay:    settings.Replay,
		Grants:    settings.Grants,
		Seals:     settings.Seals,
		Now:       settings.Now,
	}

	reported := &recorder{}
	conformance.RunWith(reported, settings, func(ctx context.Context, token string) error {
		_, err := forgot.Verify(ctx, token)
		return err
	})

	require.NotEmpty(t, reported.fatal)
	require.Contains(t, reported.fatal[len(reported.fatal)-1], "TrustTheConformanceFixtureKey")
}
