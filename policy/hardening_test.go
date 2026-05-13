package policy_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// =====================================================================
// TokenRevocationList
// =====================================================================

func TestTRL_Empty_NothingRevoked(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	require.False(t, trl.IsRevoked("anything"))
	require.Equal(t, 0, trl.Size())
}

func TestTRL_Add_MarksRevoked(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.Add("tok-1")
	require.True(t, trl.IsRevoked("tok-1"))
	require.False(t, trl.IsRevoked("tok-2"))
	require.Equal(t, 1, trl.Size())
}

func TestTRL_Add_Idempotent(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.Add("tok-1")
	trl.Add("tok-1")
	require.Equal(t, 1, trl.Size(), "double-Add must not duplicate")
}

func TestTRL_Add_Empty_Ignored(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.Add("")
	require.Equal(t, 0, trl.Size(), "empty id is meaningless; must be ignored")
}

func TestTRL_AddMany(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.AddMany([]string{"a", "b", "c", ""})
	require.Equal(t, 3, trl.Size(), "empty ids in bulk also ignored")
	require.True(t, trl.IsRevoked("a"))
	require.True(t, trl.IsRevoked("b"))
	require.True(t, trl.IsRevoked("c"))
}

func TestTRL_Remove(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.Add("tok-1")
	require.True(t, trl.IsRevoked("tok-1"))
	trl.Remove("tok-1")
	require.False(t, trl.IsRevoked("tok-1"))
}

func TestTRL_Replace_AtomicallySwapsSet(t *testing.T) {
	trl := policy.NewTokenRevocationList()
	trl.AddMany([]string{"old-1", "old-2"})
	trl.Replace([]string{"new-1", "new-2", "new-3"})

	require.False(t, trl.IsRevoked("old-1"))
	require.False(t, trl.IsRevoked("old-2"))
	require.True(t, trl.IsRevoked("new-1"))
	require.True(t, trl.IsRevoked("new-2"))
	require.True(t, trl.IsRevoked("new-3"))
	require.Equal(t, 3, trl.Size())
}

func TestTRL_Concurrent(t *testing.T) {
	// Race detector validates the locking. Many writers + many
	// readers, no panic / data race.
	trl := policy.NewTokenRevocationList()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			trl.Add(string(rune('a'+i%26)) + "-revoked")
		}()
		go func() {
			defer wg.Done()
			_ = trl.IsRevoked("a-revoked")
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, trl.Size(), 26)
}

// =====================================================================
// Break-glass
// =====================================================================

func TestBreakGlass_NotSet_Inactive(t *testing.T) {
	t.Setenv(policy.EnvBreakGlass, "")
	policy.ResetHardeningCachesForTest()

	require.False(t, policy.IsBreakGlassActive())
}

func TestBreakGlass_NonEmptyValue_Active(t *testing.T) {
	t.Setenv(policy.EnvBreakGlass, "INC-1234 emergency hotfix")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.True(t, policy.IsBreakGlassActive())
}

func TestBreakGlass_LogUsage_NoOpWhenInactive(t *testing.T) {
	t.Setenv(policy.EnvBreakGlass, "")
	policy.ResetHardeningCachesForTest()

	// Nothing to assert other than "doesn't panic" — the WARN
	// log is observable via wool but verifying log output here
	// is fragile. The behavior is the contract: when inactive,
	// it returns silently.
	require.NotPanics(t, func() {
		policy.LogBreakGlassUsage(context.Background(), "git.status", "")
	})
}

func TestBreakGlass_LogUsage_WithActive(t *testing.T) {
	t.Setenv(policy.EnvBreakGlass, "incident: locked out by misconfigured PDP")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	// The WARN log surface is exercised. We can't easily
	// capture wool output in this test setup; the assertion is
	// that the call doesn't panic and IsBreakGlassActive
	// reports true throughout.
	require.True(t, policy.IsBreakGlassActive())
	policy.LogBreakGlassUsage(context.Background(), "git.merge", "repo:foo")
	policy.LogBreakGlassUsage(context.Background(), "git.push", "repo:foo")
	require.True(t, policy.IsBreakGlassActive(),
		"break-glass stays active throughout the process — multiple uses don't change it")
}

func TestBreakGlass_LogUsage_PrincipalContext(t *testing.T) {
	t.Setenv(policy.EnvBreakGlass, "test")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	ctx := policy.WithPrincipal(context.Background(), &policy.Principal{
		ID: "u-emerg", Kind: policy.KindHuman, OrgID: "org",
	})
	require.NotPanics(t, func() {
		policy.LogBreakGlassUsage(ctx, "x", "y")
	})
}

// =====================================================================
// Recursion depth caps
// =====================================================================

func TestMaxDelegationDepth_Default(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.Equal(t, policy.DefaultMaxDelegationDepth, policy.MaxDelegationDepth())
}

func TestMaxDelegationDepth_EnvOverride(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "5")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.Equal(t, 5, policy.MaxDelegationDepth())
}

func TestMaxDelegationDepth_BadValue_FallsBackToDefault(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "not-a-number")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.Equal(t, policy.DefaultMaxDelegationDepth, policy.MaxDelegationDepth())
}

func TestMaxDelegationDepth_NegativeValue_FallsBackToDefault(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "-1")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.Equal(t, policy.DefaultMaxDelegationDepth, policy.MaxDelegationDepth())
}

func TestMaxDelegationDepth_Zero_FallsBackToDefault(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "0")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	require.Equal(t, policy.DefaultMaxDelegationDepth, policy.MaxDelegationDepth(),
		"zero is not a meaningful cap; defensive fallback")
}

func TestCheckDelegationDepth_Nil_Pass(t *testing.T) {
	require.NoError(t, policy.CheckDelegationDepth(nil))
}

func TestCheckDelegationDepth_EmptyChain_Pass(t *testing.T) {
	p := &policy.Principal{ID: "u", Kind: policy.KindHuman}
	require.NoError(t, policy.CheckDelegationDepth(p))
}

func TestCheckDelegationDepth_AtCap_Pass(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	// Default cap is 3.
	chain := []policy.DelegationLink{
		{PrincipalID: "a"},
		{PrincipalID: "b"},
		{PrincipalID: "c"},
	}
	p := &policy.Principal{ID: "u", Kind: policy.KindHuman, DelegationChain: chain}
	require.NoError(t, policy.CheckDelegationDepth(p))
}

func TestCheckDelegationDepth_OverCap_Rejected(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	// Default cap 3; chain of 4 must reject.
	chain := []policy.DelegationLink{
		{PrincipalID: "a"},
		{PrincipalID: "b"},
		{PrincipalID: "c"},
		{PrincipalID: "d"},
	}
	p := &policy.Principal{ID: "u", Kind: policy.KindHuman, DelegationChain: chain}
	err := policy.CheckDelegationDepth(p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds cap")
}

func TestCheckDelegationDepth_EnvCap_Honored(t *testing.T) {
	t.Setenv(policy.EnvMaxDelegationDepth, "1")
	policy.ResetHardeningCachesForTest()
	defer policy.ResetHardeningCachesForTest()

	// Cap 1 → chain of 2 must reject.
	chain := []policy.DelegationLink{
		{PrincipalID: "a"},
		{PrincipalID: "b"},
	}
	p := &policy.Principal{ID: "u", Kind: policy.KindHuman, DelegationChain: chain}
	err := policy.CheckDelegationDepth(p)
	require.Error(t, err)
}

// =====================================================================
// ResetHardeningCachesForTest
// =====================================================================

func TestResetHardeningCachesForTest_Idempotent(t *testing.T) {
	require.NotPanics(t, func() {
		policy.ResetHardeningCachesForTest()
		policy.ResetHardeningCachesForTest()
	})
}
