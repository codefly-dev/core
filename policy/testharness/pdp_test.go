package testharness_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

func TestFakePDP_Default_Allow(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.True(t, d.Allow, "default-allow PDP must allow with no rules")
	require.Equal(t, 1, pdp.CallCount())
}

func TestFakePDP_Default_Deny(t *testing.T) {
	pdp := testharness.NewFakeDeny()
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.False(t, d.Allow)
	require.Contains(t, d.Reason, "fake-pdp",
		"deny reason must identify the test fixture, not look like production")
}

func TestFakePDP_AllowToolbox_AllowsAllItsTools(t *testing.T) {
	pdp := testharness.NewFakeDeny().AllowToolbox("git")

	d1 := pdp.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "git", Tool: "git.status"})
	require.True(t, d1.Allow)

	d2 := pdp.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "git", Tool: "git.commit"})
	require.True(t, d2.Allow)

	// Different toolbox falls through to default-deny
	d3 := pdp.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "docker", Tool: "docker.ps"})
	require.False(t, d3.Allow)
}

func TestFakePDP_DenyTool_BlocksOnlyThatTool(t *testing.T) {
	pdp := testharness.NewFakeAllow().
		DenyTool("git", "git.force_push", "force-push forbidden in test")

	t.Run("denied tool returns deny reason", func(t *testing.T) {
		d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
			Toolbox: "git", Tool: "git.force_push",
		})
		require.False(t, d.Allow)
		require.Equal(t, "force-push forbidden in test", d.Reason)
	})

	t.Run("other git tools still allowed", func(t *testing.T) {
		d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
			Toolbox: "git", Tool: "git.status",
		})
		require.True(t, d.Allow)
	})
}

func TestFakePDP_DenyTool_EmptyReasonPanics(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	require.Panics(t, func() { pdp.DenyTool("git", "git.commit", "") },
		"silent denials harm test debuggability — must panic")
}

func TestFakePDP_RuleOrder_FirstMatchWins(t *testing.T) {
	// Specific deny BEFORE blanket allow.
	pdp := testharness.NewFakeDeny().
		DenyTool("git", "git.force_push", "specific deny").
		AllowToolbox("git")

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.force_push",
	})
	require.False(t, d.Allow, "first matching rule wins; specific deny must beat blanket allow")
	require.Equal(t, "specific deny", d.Reason)
}

func TestFakePDP_RuleOrder_BlanketBeatsLater(t *testing.T) {
	// Blanket allow BEFORE specific deny.
	pdp := testharness.NewFakeDeny().
		AllowToolbox("git").
		DenyTool("git", "git.force_push", "specific deny that comes too late")

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.force_push",
	})
	require.True(t, d.Allow,
		"first match wins — test author chose to put blanket first; honor it")
}

func TestFakePDP_Calls_RecordsArgsAndIdentity(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox:  "git",
		Tool:     "git.commit",
		Args:     map[string]any{"message": "WIP"},
		Identity: map[string]any{"principal_id": "p-1"},
	})

	calls := pdp.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "git", calls[0].Toolbox)
	require.Equal(t, "git.commit", calls[0].Tool)
	require.Equal(t, "WIP", calls[0].Args["message"])
	require.Equal(t, "p-1", calls[0].Identity["principal_id"])
	require.True(t, calls[0].Decision.Allow)
}

func TestFakePDP_Calls_SnapshotIndependentOfMutation(t *testing.T) {
	// If a caller mutates the original Args map AFTER Evaluate, the
	// recorded FakeCall must still show what was passed in. Otherwise
	// tests become flaky depending on call ordering.
	pdp := testharness.NewFakeAllow()
	args := map[string]any{"k": "before"}
	pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "x", Tool: "x.y", Args: args,
	})

	args["k"] = "after"

	calls := pdp.Calls()
	require.Equal(t, "before", calls[0].Args["k"],
		"recorded args must be a snapshot, not a live reference")
}

func TestFakePDP_LastCall_PanicsWhenEmpty(t *testing.T) {
	pdp := testharness.NewFakeAllow()
	require.Panics(t, func() { pdp.LastCall() })
}

func TestFakePDP_Reset_ClearsCallsKeepsRules(t *testing.T) {
	pdp := testharness.NewFakeDeny().AllowToolbox("git")
	pdp.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "git", Tool: "git.status"})
	require.Equal(t, 1, pdp.CallCount())

	pdp.Reset()
	require.Equal(t, 0, pdp.CallCount(), "Reset must clear calls")

	// Rules persist after reset.
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "git", Tool: "git.status"})
	require.True(t, d.Allow, "Reset must NOT clear rules")
}

func TestFakePDP_Concurrent(t *testing.T) {
	// Race detector validates the mutex; this also ensures the call
	// count is exact under load.
	pdp := testharness.NewFakeAllow()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pdp.Evaluate(context.Background(), &policy.PDPRequest{
				Toolbox: "git", Tool: "git.status",
			})
		}()
	}
	wg.Wait()
	require.Equal(t, N, pdp.CallCount())
}

func TestFakePDP_ImplementsPDPInterface(t *testing.T) {
	// Compile-time check is the assertion at the bottom of pdp.go.
	// This test exists so a refactor that breaks the assertion fails
	// here too, with a clearer error.
	var _ policy.PDP = testharness.NewFakeAllow()
}

func TestPrincipalBuilder_Defaults_ValidHuman(t *testing.T) {
	p := testharness.NewPrincipalBuilder().Build()
	require.Equal(t, policy.KindHuman, p.Kind)
	require.Equal(t, "test-org", p.OrgID)
	require.NotEmpty(t, p.ID)
	require.NotEmpty(t, p.DisplayName)
}

func TestPrincipalBuilder_UniqueIDs(t *testing.T) {
	a := testharness.NewPrincipalBuilder().Build()
	b := testharness.NewPrincipalBuilder().Build()
	require.NotEqual(t, a.ID, b.ID, "default IDs must be unique across builders")
}

func TestPrincipalBuilder_AsAgent_BuildsCanonicalIdentifier(t *testing.T) {
	p := testharness.NewPrincipalBuilder().
		AsAgent("auto-merge", "0.1.0").
		Build()
	require.Equal(t, policy.KindAgent, p.Kind)
	require.Equal(t, "test.codefly.dev/auto-merge:0.1.0", p.AgentID)
}

func TestPrincipalBuilder_AsAgent_PanicsOnEmptyName(t *testing.T) {
	require.Panics(t, func() {
		testharness.NewPrincipalBuilder().AsAgent("", "0.1.0")
	})
	require.Panics(t, func() {
		testharness.NewPrincipalBuilder().AsAgent("name", "")
	})
}

func TestPrincipalBuilder_AsService_ClearsAgentFields(t *testing.T) {
	p := testharness.NewPrincipalBuilder().
		AsAgent("foo", "0.1.0").
		AsService(). // overrides
		Build()
	require.Equal(t, policy.KindService, p.Kind)
	require.Empty(t, p.AgentID, "AsService must clear AgentID set by prior AsAgent")
}

func TestPrincipalBuilder_ExpiringIn(t *testing.T) {
	before := time.Now()
	p := testharness.NewPrincipalBuilder().ExpiringIn(time.Hour).Build()
	after := time.Now()

	require.True(t, p.ExpiresAt.After(before.Add(59*time.Minute)))
	require.True(t, p.ExpiresAt.Before(after.Add(time.Hour+time.Second)))
}

func TestPrincipalBuilder_DelegatedFrom_BuildsChain(t *testing.T) {
	p := testharness.NewPrincipalBuilder().
		AsAgent("merger", "0.1.0").
		DelegatedFrom("u-1", policy.KindHuman, "antoine", "g-100").
		DelegatedFrom("a-mind", policy.KindAgent, "Mind", "g-101").
		Build()
	require.Len(t, p.DelegationChain, 2)
	require.Equal(t, "u-1", p.DelegationChain[0].PrincipalID)
	require.Equal(t, "g-100", p.DelegationChain[0].GrantID)
	require.Equal(t, "a-mind", p.DelegationChain[1].PrincipalID)
}

func TestPrincipalBuilder_BuildPanicsOnInvalid(t *testing.T) {
	// Force an invalid combo by using WithAgentID with an empty value
	// after AsHuman. The Validate call inside Build should panic.
	require.Panics(t, func() {
		// Bypass via direct field manipulation — simulating a misuse.
		// We can't easily construct an invalid Principal through the
		// fluent API; this test exists to lock in the invariant that
		// Build calls Validate.
		b := testharness.NewPrincipalBuilder()
		b.WithID("") // zero ID is invalid
		b.Build()
	})
}
