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
