package policy_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// =====================================================================
// Mint
// =====================================================================

func TestMint_HappyPath_ProducesEncodableToken(t *testing.T) {
	secret := policy.NewSpawnSecret()
	p := &policy.Principal{
		ID: "u-antoine", Kind: policy.KindHuman, OrgID: "org-codefly",
	}
	encoded, sa, err := policy.Mint(policy.MintInput{
		Principal:  p,
		Action:     "github.merge_pr",
		Resource:   "repo:codefly/codefly.dev",
		AudienceID: "codefly.dev/github-bot:0.1.0",
		TTL:        2 * time.Minute,
	}, secret)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
	require.Contains(t, encoded, ".",
		"token wire format is <envelope>.<sig>")

	require.NotNil(t, sa)
	require.NotEmpty(t, sa.ID, "every minted token has a unique id for audit")
	require.Equal(t, "github.merge_pr", sa.Action)
	require.Equal(t, "v1-hmac", sa.Format)
	require.Equal(t, 1, sa.MaxUses, "MaxUses defaults to 1 (single-shot)")
}

func TestMint_NilPrincipal_Errors(t *testing.T) {
	_, _, err := policy.Mint(policy.MintInput{
		Action: "git.status", TTL: time.Minute,
	}, policy.NewSpawnSecret())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}

func TestMint_EmptyAction_Errors(t *testing.T) {
	p := &policy.Principal{ID: "u-x", Kind: policy.KindHuman}
	_, _, err := policy.Mint(policy.MintInput{
		Principal: p, TTL: time.Minute,
	}, policy.NewSpawnSecret())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "action")
}

func TestMint_ZeroTTL_Errors(t *testing.T) {
	p := &policy.Principal{ID: "u-x", Kind: policy.KindHuman}
	_, _, err := policy.Mint(policy.MintInput{
		Principal: p, Action: "x.y", TTL: 0,
	}, policy.NewSpawnSecret())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "TTL")
}

func TestMint_ShortSecret_Errors(t *testing.T) {
	p := &policy.Principal{ID: "u-x", Kind: policy.KindHuman}
	_, _, err := policy.Mint(policy.MintInput{
		Principal: p, Action: "x.y", TTL: time.Minute,
	}, []byte("too-short"))
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "32 bytes")
}

func TestMint_InvalidPrincipal_Errors(t *testing.T) {
	// Agent without AgentID — invalid per Principal.Validate.
	p := &policy.Principal{ID: "u-x", Kind: policy.KindAgent, OrgID: "o"}
	_, _, err := policy.Mint(policy.MintInput{
		Principal: p, Action: "x.y", TTL: time.Minute,
	}, policy.NewSpawnSecret())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}

func TestMint_NegativeMaxUses_Errors(t *testing.T) {
	p := &policy.Principal{ID: "u-x", Kind: policy.KindHuman}
	_, _, err := policy.Mint(policy.MintInput{
		Principal: p, Action: "x.y", TTL: time.Minute, MaxUses: -1,
	}, policy.NewSpawnSecret())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}

func TestMint_UniqueIDs(t *testing.T) {
	secret := policy.NewSpawnSecret()
	p := &policy.Principal{ID: "u-x", Kind: policy.KindHuman}
	ids := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		_, sa, err := policy.Mint(policy.MintInput{
			Principal: p, Action: "x.y", TTL: time.Minute,
		}, secret)
		require.NoError(t, err)
		_, dup := ids[sa.ID]
		require.False(t, dup, "ULID collision in 100 mints — entropy issue")
		ids[sa.ID] = struct{}{}
	}
}

// =====================================================================
// Verify
// =====================================================================

func mustMint(t *testing.T, in policy.MintInput, secret []byte) string {
	t.Helper()
	encoded, _, err := policy.Mint(in, secret)
	require.NoError(t, err)
	return encoded
}

func defaultPrincipal() *policy.Principal {
	return &policy.Principal{
		ID: "u-antoine", Kind: policy.KindHuman, OrgID: "org-codefly",
	}
}

func TestVerify_HappyPath(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal:  defaultPrincipal(),
		Action:     "github.merge_pr",
		Resource:   "repo:codefly/x",
		AudienceID: "codefly.dev/github-bot:0.1.0",
		TTL:        time.Minute,
	}, secret)

	sa, err := policy.Verify(token, policy.VerifyExpectations{
		Action:      "github.merge_pr",
		Resource:    "repo:codefly/x",
		Audience:    "codefly.dev/github-bot:0.1.0",
		PrincipalID: "u-antoine",
	}, secret)
	require.NoError(t, err)
	require.NotNil(t, sa)
	require.Equal(t, "u-antoine", sa.PrincipalID)
}

func TestVerify_TamperedSignature_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
	}, secret)

	// Flip a byte in the signature, KEEPING the rest intact so
	// the base64 stays decodable. Tests only the HMAC-mismatch
	// branch, not the malformed-base64 branch.
	dot := strings.IndexByte(token, '.')
	require.Greater(t, dot, 0)
	tampered := token[:dot+1] + flipChar(token[dot+1]) + token[dot+2:]
	require.NotEqual(t, token, tampered)

	_, err := policy.Verify(tampered, policy.VerifyExpectations{Action: "x.y"}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "signature")
}

func TestVerify_TamperedEnvelope_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "github.read_pr", TTL: time.Minute,
	}, secret)

	// Flip a byte in the envelope, mid-stream so base64 stays
	// well-formed but the bytes differ.
	tampered := token[:5] + flipChar(token[5]) + token[6:]
	_, err := policy.Verify(tampered, policy.VerifyExpectations{Action: "github.read_pr"}, secret)
	require.Error(t, err, "tampered envelope must fail HMAC verify")
}

func TestVerify_WrongSecret_Rejected(t *testing.T) {
	secretA := policy.NewSpawnSecret()
	secretB := policy.NewSpawnSecret()

	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
	}, secretA)

	_, err := policy.Verify(token, policy.VerifyExpectations{Action: "x.y"}, secretB)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "signature")
}

func TestVerify_Expired_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: 100 * time.Millisecond,
	}, secret)

	// Wait for expiry + skew.
	time.Sleep(200*time.Millisecond + 31*time.Second/30) // > expire + minimal grace; manual

	// Use an explicit Now to make this deterministic.
	now := time.Now().Add(time.Minute) // way past skew
	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y",
		Now:    now,
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "expired")
}

func TestVerify_FutureIssued_Rejected(t *testing.T) {
	// Mint with a clock far in the future, verify with current time.
	// Should reject (clock skew exceeded).
	future := func() time.Time { return time.Now().Add(time.Hour) }
	secret := policy.NewSpawnSecret()
	encoded, _, err := policy.Mint(policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
		NowFunc: future,
	}, secret)
	require.NoError(t, err)

	_, err = policy.Verify(encoded, policy.VerifyExpectations{Action: "x.y"}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "future")
}

func TestVerify_ActionMismatch_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "github.read_pr", TTL: time.Minute,
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "github.merge_pr", // ← different
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "action mismatch")
}

func TestVerify_ResourceMismatch_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y",
		Resource:  "repo:codefly/x",
		TTL:       time.Minute,
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action:   "x.y",
		Resource: "repo:other/y",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "resource mismatch")
}

func TestVerify_AudienceMismatch_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
		AudienceID: "plugin-A",
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action:   "x.y",
		Audience: "plugin-B",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "audience")
}

func TestVerify_EmptyAudienceCannotSatisfyBoundExpectation(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(), Action: "x.y", TTL: time.Minute,
	}, secret)
	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y", Audience: "codefly.dev/fixture:1.0.0",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "audience mismatch")
}

func TestVerify_UnboundResourceCannotSatisfyResourceExpectation(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(), Action: "x.y", TTL: time.Minute,
	}, secret)
	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y", Resource: "database:tenant-1",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "resource mismatch")
}

func TestVerify_PrincipalMismatch_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action:      "x.y",
		PrincipalID: "u-someone-else",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "principal")
}

func TestVerify_PrincipalKindAndOrganizationMismatchRejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(), Action: "x.y", TTL: time.Minute,
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y", PrincipalKind: policy.KindAgent,
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "principal kind")

	_, err = policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y", OrganizationID: "org-other",
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "organization")
}

func TestVerify_EmptyExpectations_SkipsChecks(t *testing.T) {
	// If expect.X is empty, the corresponding check is skipped.
	// Used by tests that only care about signature/expiry.
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "specific.action",
		Resource:  "specific:resource",
		TTL:       time.Minute,
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{}, secret)
	require.NoError(t, err)
}

func TestVerify_Malformed_Rejected(t *testing.T) {
	secret := policy.NewSpawnSecret()
	for _, bad := range []string{
		"",
		"no-dot-here",
		".",
		"abc.",
		".xyz",
		"!!!.!!!",
	} {
		_, err := policy.Verify(bad, policy.VerifyExpectations{}, secret)
		require.Error(t, err, "malformed token %q must reject", bad)
	}
}

// =====================================================================
// Caveats
// =====================================================================

func TestVerify_KnownCaveat_VerifierAccepts(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
		Caveats: map[string]any{
			"ci_status": "green",
		},
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y",
		CaveatVerifiers: map[string]policy.CaveatVerifier{
			"ci_status": func(v any) error {
				s, _ := v.(string)
				if s != "green" {
					return errors.New("expected green")
				}
				return nil
			},
		},
	}, secret)
	require.NoError(t, err)
}

func TestVerify_KnownCaveat_VerifierRejects(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
		Caveats: map[string]any{
			"ci_status": "red",
		},
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y",
		CaveatVerifiers: map[string]policy.CaveatVerifier{
			"ci_status": func(v any) error {
				if v.(string) != "green" {
					return errors.New("CI not green")
				}
				return nil
			},
		},
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "ci_status")
}

func TestVerify_UnknownCaveat_DeniesByDefault(t *testing.T) {
	// SECURITY-CRITICAL: an unknown caveat key MUST cause
	// verification to fail. An attacker could try to encode a
	// caveat the verifier doesn't understand, hoping it's
	// silently ignored.
	secret := policy.NewSpawnSecret()
	token := mustMint(t, policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "x.y", TTL: time.Minute,
		Caveats: map[string]any{
			"unknown_caveat": "anything",
		},
	}, secret)

	_, err := policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y",
		// No CaveatVerifiers registered for unknown_caveat.
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.Contains(t, err.Error(), "unknown caveat",
		"unknown caveats deny by default — security default")
}

// =====================================================================
// ReplayTracker
// =====================================================================

func TestReplayTracker_FirstUse_Accepts(t *testing.T) {
	tracker := policy.NewReplayTracker()
	sa := &policy.ScopedAuthorization{
		ID:            "tok-1",
		MaxUses:       1,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, tracker.Consume(sa))
}

func TestReplayTracker_SecondUse_OnSingleShot_Rejects(t *testing.T) {
	tracker := policy.NewReplayTracker()
	sa := &policy.ScopedAuthorization{
		ID:            "tok-1",
		MaxUses:       1,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, tracker.Consume(sa))
	err := tracker.Consume(sa)
	require.ErrorIs(t, err, policy.ErrScopedAuthExhausted,
		"single-shot token replayed must reject")
}

func TestReplayTracker_MaxUsesN_AllowsExactlyN(t *testing.T) {
	tracker := policy.NewReplayTracker()
	sa := &policy.ScopedAuthorization{
		ID:            "tok-bulk",
		MaxUses:       3,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, tracker.Consume(sa))                                // 1
	require.NoError(t, tracker.Consume(sa))                                // 2
	require.NoError(t, tracker.Consume(sa))                                // 3
	require.ErrorIs(t, tracker.Consume(sa), policy.ErrScopedAuthExhausted) // 4 — over
}

func TestReplayTracker_DistinctIDs_Independent(t *testing.T) {
	tracker := policy.NewReplayTracker()
	sa1 := &policy.ScopedAuthorization{
		ID: "tok-1", MaxUses: 1,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	sa2 := &policy.ScopedAuthorization{
		ID: "tok-2", MaxUses: 1,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, tracker.Consume(sa1))
	require.NoError(t, tracker.Consume(sa2),
		"distinct token ids tracked independently")
}

func TestReplayTracker_Concurrent_AccurateCount(t *testing.T) {
	tracker := policy.NewReplayTracker()
	sa := &policy.ScopedAuthorization{
		ID: "tok-c", MaxUses: 100,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}

	var (
		wg          sync.WaitGroup
		successes   = make(chan struct{}, 200)
		failures    = make(chan struct{}, 200)
		concurrency = 200 // > MaxUses
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tracker.Consume(sa); err == nil {
				successes <- struct{}{}
			} else {
				failures <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(successes)
	close(failures)

	successCount := len(successes)
	failureCount := len(failures)
	require.Equal(t, 100, successCount,
		"under concurrent contention, exactly MaxUses=100 must succeed")
	require.Equal(t, concurrency-100, failureCount,
		"the rest must reject with Exhausted")
}

func TestReplayTracker_Pruning_RemovesExpired(t *testing.T) {
	tracker := policy.NewReplayTracker()
	expired := &policy.ScopedAuthorization{
		ID: "tok-old", MaxUses: 1,
		ExpiresAtUnix: time.Now().Add(-time.Minute).Unix(),
	}
	// Consuming an expired token still records (the verifier
	// catches expiry; the tracker is permissive). But on the
	// NEXT consume of any token, the entry should be pruned.
	require.NoError(t, tracker.Consume(expired))
	require.Equal(t, 1, tracker.Size())

	fresh := &policy.ScopedAuthorization{
		ID: "tok-new", MaxUses: 1,
		ExpiresAtUnix: time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, tracker.Consume(fresh))
	// pruneLocked ran; expired entry should be gone, fresh remains.
	require.Equal(t, 1, tracker.Size(),
		"expired entries pruned on next Consume")
}

func TestReplayTracker_RetainsUseThroughVerificationClockSkew(t *testing.T) {
	tracker := policy.NewReplayTracker()
	token := &policy.ScopedAuthorization{
		ID: "tok-within-skew", MaxUses: 1,
		ExpiresAtUnix: time.Now().Add(-10 * time.Second).Unix(),
	}

	require.NoError(t, tracker.Consume(token))
	require.ErrorIs(t, tracker.Consume(token), policy.ErrScopedAuthExhausted,
		"a token accepted during clock skew must not regain a use after its nominal expiry")
}

// =====================================================================
// Context plumbing
// =====================================================================

func TestScopedAuthFromContext_Empty(t *testing.T) {
	require.Nil(t, policy.ScopedAuthFrom(context.Background()))
	require.Nil(t, policy.ScopedAuthFrom(nil)) //nolint:staticcheck // intentional nil
}

func TestWithScopedAuth_RoundTrip(t *testing.T) {
	sa := &policy.ScopedAuthorization{ID: "tok-x"}
	ctx := policy.WithScopedAuth(context.Background(), sa)
	require.Same(t, sa, policy.ScopedAuthFrom(ctx))
}

// =====================================================================
// Helpers
// =====================================================================

// flipChar returns a different character with the same encoding
// length (used for tampering tests). For base64url chars, just
// pick one that's different.
func flipChar(c byte) string {
	if c == 'a' {
		return "b"
	}
	return "a"
}

// =====================================================================
// NewSpawnSecret
// =====================================================================

func TestNewSpawnSecret_ReturnsThirtyTwoBytes(t *testing.T) {
	s := policy.NewSpawnSecret()
	require.Len(t, s, 32)
}

func TestNewSpawnSecret_DistinctEachCall(t *testing.T) {
	a := policy.NewSpawnSecret()
	b := policy.NewSpawnSecret()
	require.NotEqual(t, a, b, "secrets must be cryptographically random")
}

// =====================================================================
// NewULID
// =====================================================================

func TestNewULID_Unique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		id := policy.NewULID()
		require.NotContains(t, seen, id, "ULID collision in 1000 iterations")
		seen[id] = struct{}{}
	}
}

func TestNewULID_TimeSortable(t *testing.T) {
	a := policy.NewULID()
	time.Sleep(2 * time.Millisecond)
	b := policy.NewULID()
	require.Less(t, a, b,
		"time-prefix must make ULIDs lexicographically sortable by time")
}
