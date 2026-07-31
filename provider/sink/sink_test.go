package sink_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/sdk"
	"github.com/codefly-dev/core/provider/sink"
	"github.com/stretchr/testify/require"
)

func newMemory(t *testing.T) *sink.Memory {
	t.Helper()
	s, err := sink.NewMemory()
	require.NoError(t, err)
	return s
}

func runtimeAddress(name string) sink.Address {
	return sink.Address{
		Consumer:    "checkout",
		Environment: "production",
		Binding:     "billing-stripe",
		Provider:    "stripe",
		Purpose:     providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		Name:        name,
	}
}

func TestPrepareReturnsDeterministicOpaqueTarget(t *testing.T) {
	s := newMemory(t)
	first, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	require.NoError(t, configuration.ValidateOpaqueReference(first.Target()))

	second, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	require.Equal(t, first.Target(), second.Target(), "re-preparing the same address must not fork a reservation")

	other, err := s.Prepare(runtimeAddress("webhook-secret"), false)
	require.NoError(t, err)
	require.NotEqual(t, first.Target(), other.Target())
}

func TestPutDurableCapturesAndHostRecovers(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	value := []byte("sk_live_capturedsecret")
	ref, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.NoError(t, configuration.ValidateOpaqueReference(ref.GetReference()))
	require.Equal(t, providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME, ref.GetPurpose())
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, ref.GetSafeFingerprint())
	require.NotContains(t, ref.GetReference(), string(value))

	recovered, err := s.Lookup(ref)
	require.NoError(t, err)
	require.True(t, bytes.Equal(value, recovered))
}

func TestPutDurableIsIdempotentUnderRetry(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	value := []byte("sk_live_capturedsecret")
	first, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	second, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.Equal(t, first.GetReference(), second.GetReference(), "a duplicate capture of the same value must not create a new version")
}

func TestPutDurableRotatesToNewVersionAndKeepsPrior(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	old := []byte("sk_live_original")
	rotated := []byte("sk_live_rotated")
	oldRef, err := s.PutDurable(reservation.Target(), old)
	require.NoError(t, err)
	newRef, err := s.PutDurable(reservation.Target(), rotated)
	require.NoError(t, err)
	require.NotEqual(t, oldRef.GetReference(), newRef.GetReference())

	// Both versions remain resolvable so a consumer that failed to adopt the new
	// reference can still be rolled back to the prior one.
	priorValue, err := s.Lookup(oldRef)
	require.NoError(t, err)
	require.True(t, bytes.Equal(old, priorValue))
	currentValue, err := s.Lookup(newRef)
	require.NoError(t, err)
	require.True(t, bytes.Equal(rotated, currentValue))
}

func TestDuplicateCaptureAfterRevokeDoesNotResurrect(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	value := []byte("sk_live_revokeme")
	ref, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.NoError(t, s.Revoke(ref))

	// A retried delivery of the same capture after revocation must be a no-op
	// that returns the existing revoked reference, not a fresh live version.
	duplicate, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.Equal(t, ref.GetReference(), duplicate.GetReference(), "duplicate capture must not fork a new version")
	_, err = s.Lookup(duplicate)
	require.Error(t, err, "the secret must stay revoked, not be resurrected")
}

func TestDuplicateCaptureAfterOneTimeConsumeDoesNotResurrect(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("webhook-secret"), true)
	require.NoError(t, err)

	value := []byte("whsec_onetime")
	ref, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	_, err = s.Lookup(ref)
	require.NoError(t, err)

	// Redelivering the same one-time capture after it was consumed must not mint
	// a second recoverable copy.
	duplicate, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.Equal(t, ref.GetReference(), duplicate.GetReference())
	_, err = s.Lookup(duplicate)
	require.Error(t, err, "a consumed one-time secret must stay consumed")
}

func TestFingerprintIsKeyedNotRawHash(t *testing.T) {
	value := []byte("1234") // low-entropy value: a raw hash would be brute-forceable
	rawSum := sha256.Sum256(value)
	rawFingerprint := "sha256:" + hex.EncodeToString(rawSum[:])

	first := newMemory(t)
	reservation, err := first.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	firstRef, err := first.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, firstRef.GetSafeFingerprint())
	require.NotEqual(t, rawFingerprint, firstRef.GetSafeFingerprint(), "fingerprint must be keyed, not a raw digest of the value")

	// Stable within one sink so duplicate capture and drift comparison work.
	stable, err := first.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	require.Equal(t, firstRef.GetSafeFingerprint(), stable.GetSafeFingerprint())

	// Keyed per instance, so the same value fingerprints differently elsewhere.
	second := newMemory(t)
	otherReservation, err := second.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	secondRef, err := second.PutDurable(otherReservation.Target(), value)
	require.NoError(t, err)
	require.NotEqual(t, firstRef.GetSafeFingerprint(), secondRef.GetSafeFingerprint())
}

func TestAbortUnusedRemovesOnlyUnfilledReservation(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	require.NoError(t, s.AbortUnused(reservation.Target()))
	// The reservation is gone, so a later put cannot land against it.
	_, err = s.PutDurable(reservation.Target(), []byte("sk_live_late"))
	require.Error(t, err)
	// Aborting again is a no-op.
	require.NoError(t, s.AbortUnused(reservation.Target()))
}

func TestAbortUnusedNeverRollsBackADurableSecret(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)

	value := []byte("sk_live_durable")
	ref, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)

	// A later action failing must not implicitly undo the capture.
	require.Error(t, s.AbortUnused(reservation.Target()))
	recovered, err := s.Lookup(ref)
	require.NoError(t, err)
	require.True(t, bytes.Equal(value, recovered))
}

func TestPutDurableRequiresReservationAndValue(t *testing.T) {
	s := newMemory(t)
	_, err := s.PutDurable("capture://unknown", []byte("value"))
	require.Error(t, err)

	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	_, err = s.PutDurable(reservation.Target(), nil)
	require.Error(t, err)
}

func TestOneTimeSecretIsConsumedOnFirstLookup(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("webhook-secret"), true)
	require.NoError(t, err)
	require.True(t, reservation.OneTime())

	ref, err := s.PutDurable(reservation.Target(), []byte("whsec_onetime"))
	require.NoError(t, err)

	value, err := s.Lookup(ref)
	require.NoError(t, err)
	require.Equal(t, "whsec_onetime", string(value))

	_, err = s.Lookup(ref)
	require.Error(t, err, "a one-time secret must not be recoverable twice")
}

func TestRevokeMakesSecretUnrecoverable(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	ref, err := s.PutDurable(reservation.Target(), []byte("sk_live_revokeme"))
	require.NoError(t, err)

	require.NoError(t, s.Revoke(ref))
	_, err = s.Lookup(ref)
	require.Error(t, err)
	require.NoError(t, s.Revoke(ref), "revoking again is a no-op")
}

func TestLookupRejectsPurposeMismatch(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	ref, err := s.PutDurable(reservation.Target(), []byte("sk_live_scoped"))
	require.NoError(t, err)

	ref.Purpose = providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT
	_, err = s.Lookup(ref)
	require.Error(t, err)
}

func TestAuditLogsOperationsWithoutValues(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	value := []byte("sk_live_neverlogged")
	ref, err := s.PutDurable(reservation.Target(), value)
	require.NoError(t, err)
	_, err = s.Lookup(ref)
	require.NoError(t, err)
	require.NoError(t, s.Revoke(ref))

	events := s.Audit()
	require.Equal(t, []sink.AuditOp{sink.OpPrepare, sink.OpPutDurable, sink.OpLookup, sink.OpRevoke},
		[]sink.AuditOp{events[0].Op, events[1].Op, events[2].Op, events[3].Op})
	for i, event := range events {
		require.Equal(t, uint64(i+1), event.Seq, "audit sequence numbers must increase by one")
		require.Equal(t, "billing-stripe", event.Binding)
		require.NotContains(t, event.Reference, string(value))
		require.NotContains(t, event.Fingerprint, string(value))
	}
}

func TestAddressValidation(t *testing.T) {
	s := newMemory(t)
	for name, addr := range map[string]sink.Address{
		"missing consumer":    {Environment: "prod", Binding: "b", Provider: "p", Purpose: providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME, Name: "n"},
		"missing purpose":     {Consumer: "c", Environment: "prod", Binding: "b", Provider: "p", Name: "n"},
		"secret-shaped scope": runtimeSecretShaped(),
	} {
		_, err := s.Prepare(addr, false)
		require.Errorf(t, err, "expected %s to be rejected", name)
	}
}

func runtimeSecretShaped() sink.Address {
	addr := runtimeAddress("n")
	addr.Provider = "sk_live_provider"
	return addr
}

func TestSinkReferenceFlowsThroughCaptureResult(t *testing.T) {
	s := newMemory(t)
	reservation, err := s.Prepare(runtimeAddress("secret-key"), false)
	require.NoError(t, err)
	ref, err := s.PutDurable(reservation.Target(), []byte("sk_live_capture"))
	require.NoError(t, err)

	result := &providerv0.CaptureResult{
		CaptureId:     "capture-1",
		Selector:      "v1:$.secret",
		SinkReference: ref,
		Captured:      true,
	}
	handled, err := sdk.HandleCaptureResult(result)
	require.NoError(t, err)
	require.Equal(t, ref.GetReference(), handled.GetReference())
}

func TestConcurrentBindingsAreIsolatedAndSafe(t *testing.T) {
	s := newMemory(t)
	var wg sync.WaitGroup
	for n := range 16 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			addr := runtimeAddress(fmt.Sprintf("secret-%d", n))
			reservation, err := s.Prepare(addr, false)
			require.NoError(t, err)
			value := fmt.Appendf(nil, "sk_live_value_%d", n)
			ref, err := s.PutDurable(reservation.Target(), value)
			require.NoError(t, err)
			recovered, err := s.Lookup(ref)
			require.NoError(t, err)
			require.True(t, bytes.Equal(value, recovered))
		}(n)
	}
	wg.Wait()
}
