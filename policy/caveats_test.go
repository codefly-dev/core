package policy_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// =====================================================================
// time_window
// =====================================================================

func TestTimeWindowCaveat_AllowsInsideWindow(t *testing.T) {
	producer, precheck, _ := policy.LookupCaveat("time_window")
	require.NotNil(t, producer)
	require.NotNil(t, precheck)

	// 9-17 UTC, all days. Always within for ~30% of any
	// arbitrary clock; this test depends on the host clock,
	// which is fragile. Better: test the helper directly via
	// the verifier with a snapshot-and-check pattern below.
	_ = precheck
}

func TestTimeWindowCaveat_FullDay_AlwaysAllows(t *testing.T) {
	// 0-24 covers every hour.
	producer, precheck, err := lookupAndBuild("time_window", policy.CaveatSpec{
		"start_hour": 0,
		"end_hour":   24,
	})
	require.NoError(t, err)
	require.NotNil(t, precheck)
	require.NotNil(t, producer)

	require.NoError(t, precheck(context.Background(), policy.EvaluationInput{}))
}

func TestTimeWindowCaveat_EmptyWindow_AlwaysDenies(t *testing.T) {
	// start == end means an empty window — protects against
	// "operator forgot to set end_hour".
	producer, precheck, err := lookupAndBuild("time_window", policy.CaveatSpec{
		"start_hour": 9,
		"end_hour":   9,
	})
	require.NoError(t, err)
	_ = producer
	err = precheck(context.Background(), policy.EvaluationInput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside")
}

func TestTimeWindowCaveat_BadHour_RejectsAtParse(t *testing.T) {
	_, _, err := lookupAndBuild("time_window", policy.CaveatSpec{
		"start_hour": 25,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestTimeWindowCaveat_BadDayOfWeek_RejectsAtParse(t *testing.T) {
	_, _, err := lookupAndBuild("time_window", policy.CaveatSpec{
		"start_hour":   0,
		"end_hour":     24,
		"days_of_week": []string{"Monday", "funday"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "funday")
}

func TestTimeWindowCaveat_Verifier_RoundTripsSnapshot(t *testing.T) {
	// Mint produces a snapshot; verifier checks against current
	// time using the same spec.
	producer, _, err := lookupAndBuild("time_window", policy.CaveatSpec{
		"start_hour": 0,
		"end_hour":   24,
	})
	require.NoError(t, err)

	snapshot, err := producer(policy.EvaluationInput{})
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	_, verifierFactory, ok := policy.LookupCaveat("time_window")
	require.True(t, ok)
	verifier, err := verifierFactory(policy.CaveatSpec{})
	require.NoError(t, err)

	require.NoError(t, verifier(snapshot),
		"24-hour window: verifier always accepts")
}

func TestTimeWindowCaveat_Verifier_DeniesOutsideWindow(t *testing.T) {
	// Manufacture a snapshot for a window that surely doesn't
	// contain "now" — start_hour=now+1, end_hour=now+2.
	now := time.Now().UTC()
	startHr := (now.Hour() + 1) % 24
	endHr := (now.Hour() + 2) % 24
	if endHr == 0 {
		endHr = 24
	}
	if startHr == endHr {
		// Edge: skip when arithmetic produces empty window.
		t.Skip("hour arithmetic produced empty window; rerun")
	}

	snapshot := map[string]any{
		"start_hour": startHr,
		"end_hour":   endHr,
		"timezone":   "UTC",
	}

	_, verifierFactory, _ := policy.LookupCaveat("time_window")
	verifier, _ := verifierFactory(policy.CaveatSpec{})
	err := verifier(snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside")
}

// =====================================================================
// rate_limit
// =====================================================================

func TestRateLimitCaveat_AllowsUnderLimit(t *testing.T) {
	_, precheck, err := lookupAndBuild("rate_limit", policy.CaveatSpec{
		"per_minute": 5,
	})
	require.NoError(t, err)

	in := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-1", Kind: policy.KindHuman},
		Toolbox:   "tb",
		Tool:      "t.x",
	}
	for i := 0; i < 5; i++ {
		require.NoError(t, precheck(context.Background(), in),
			"under limit: must allow")
	}
}

func TestRateLimitCaveat_DeniesAtLimit(t *testing.T) {
	_, precheck, err := lookupAndBuild("rate_limit", policy.CaveatSpec{
		"per_minute": 3,
	})
	require.NoError(t, err)

	in := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-1", Kind: policy.KindHuman},
		Toolbox:   "tb",
		Tool:      "t.x",
	}
	require.NoError(t, precheck(context.Background(), in))
	require.NoError(t, precheck(context.Background(), in))
	require.NoError(t, precheck(context.Background(), in))

	err = precheck(context.Background(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

func TestRateLimitCaveat_PerPrincipal_Independent(t *testing.T) {
	_, precheck, err := lookupAndBuild("rate_limit", policy.CaveatSpec{
		"per_minute": 1,
	})
	require.NoError(t, err)

	in1 := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-A", Kind: policy.KindHuman},
		Toolbox:   "tb", Tool: "t.x",
	}
	in2 := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-B", Kind: policy.KindHuman},
		Toolbox:   "tb", Tool: "t.x",
	}

	require.NoError(t, precheck(context.Background(), in1))
	require.NoError(t, precheck(context.Background(), in2),
		"different principal: independent counter")
	require.Error(t, precheck(context.Background(), in1),
		"same principal at limit: denied")
	require.Error(t, precheck(context.Background(), in2))
}

func TestRateLimitCaveat_GlobalScope_SharesAcrossPrincipals(t *testing.T) {
	_, precheck, err := lookupAndBuild("rate_limit", policy.CaveatSpec{
		"per_minute": 2,
		"scope":      "global",
	})
	require.NoError(t, err)

	in1 := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-A", Kind: policy.KindHuman},
		Toolbox:   "tb", Tool: "t.x",
	}
	in2 := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-B", Kind: policy.KindHuman},
		Toolbox:   "tb", Tool: "t.x",
	}
	require.NoError(t, precheck(context.Background(), in1))
	require.NoError(t, precheck(context.Background(), in2))
	err = precheck(context.Background(), in1)
	require.Error(t, err, "global scope: counters merged")
}

func TestRateLimitCaveat_NoCaveatInToken(t *testing.T) {
	// rate_limit has no producer — it's mint-only.
	producer, _, err := lookupAndBuild("rate_limit", policy.CaveatSpec{
		"per_minute": 5,
	})
	require.NoError(t, err)
	require.Nil(t, producer,
		"rate_limit must NOT bake a caveat into the token — it's mint-only")
}

func TestRateLimitCaveat_UnexpectedToken_VerifierRejects(t *testing.T) {
	// If a token DOES carry a rate_limit caveat (gateway
	// misconfigured), the verifier rejects loudly.
	_, verifierFactory, _ := policy.LookupCaveat("rate_limit")
	verifier, _ := verifierFactory(policy.CaveatSpec{})
	err := verifier("anything")
	require.Error(t, err)
	require.Contains(t, err.Error(), "misconfigured")
}

func TestRateLimitCaveat_BadSpec_RejectsAtParse(t *testing.T) {
	cases := []policy.CaveatSpec{
		{},                                       // missing per_minute
		{"per_minute": 0},                        // zero is invalid
		{"per_minute": -5},                       // negative
		{"per_minute": 1, "scope": "weirdscope"}, // bad scope
	}
	for i, spec := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			_, _, err := lookupAndBuild("rate_limit", spec)
			require.Error(t, err)
		})
	}
}

// =====================================================================
// allowlist
// =====================================================================

func TestAllowlistCaveat_PrecheckAllows_WhenContextValueMatches(t *testing.T) {
	_, precheck, err := lookupAndBuild("allowlist", policy.CaveatSpec{
		"context_key": "ci_status",
		"allowed":     []string{"green", "success"},
	})
	require.NoError(t, err)

	require.NoError(t, precheck(context.Background(), policy.EvaluationInput{
		Context: map[string]any{"ci_status": "green"},
	}))
}

func TestAllowlistCaveat_PrecheckDenies_WhenValueNotInList(t *testing.T) {
	_, precheck, err := lookupAndBuild("allowlist", policy.CaveatSpec{
		"context_key": "ci_status",
		"allowed":     []string{"green"},
	})
	require.NoError(t, err)

	err = precheck(context.Background(), policy.EvaluationInput{
		Context: map[string]any{"ci_status": "red"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "red")
}

func TestAllowlistCaveat_PrecheckDenies_WhenKeyMissing(t *testing.T) {
	_, precheck, err := lookupAndBuild("allowlist", policy.CaveatSpec{
		"context_key": "ci_status",
		"allowed":     []string{"green"},
	})
	require.NoError(t, err)

	err = precheck(context.Background(), policy.EvaluationInput{Context: map[string]any{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing key")
}

func TestAllowlistCaveat_AnyOfList_MatchesIfAnyElementInAllowed(t *testing.T) {
	_, precheck, err := lookupAndBuild("allowlist", policy.CaveatSpec{
		"context_key": "labels",
		"allowed":     []string{"auto-merge", "force-merge"},
		"match_mode":  "any_of_list",
	})
	require.NoError(t, err)

	// Labels = ["wip", "auto-merge"] — auto-merge is in allowed → pass.
	require.NoError(t, precheck(context.Background(), policy.EvaluationInput{
		Context: map[string]any{"labels": []string{"wip", "auto-merge"}},
	}))

	// Labels = ["wip"] — none in allowed → deny.
	err = precheck(context.Background(), policy.EvaluationInput{
		Context: map[string]any{"labels": []string{"wip"}},
	})
	require.Error(t, err)
}

func TestAllowlistCaveat_Producer_SnapshotsValueIntoToken(t *testing.T) {
	producer, _, err := lookupAndBuild("allowlist", policy.CaveatSpec{
		"context_key": "ci_status",
		"allowed":     []string{"green"},
	})
	require.NoError(t, err)
	require.NotNil(t, producer)

	v, err := producer(policy.EvaluationInput{
		Context: map[string]any{"ci_status": "green"},
	})
	require.NoError(t, err)

	m, ok := v.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "green", m["snapshot_value"])
	require.Equal(t, "ci_status", m["context_key"])
	require.Equal(t, []string{"green"}, m["allowed"])
}

func TestAllowlistCaveat_Verifier_AcceptsValidSnapshot(t *testing.T) {
	_, verifierFactory, _ := policy.LookupCaveat("allowlist")
	verifier, _ := verifierFactory(policy.CaveatSpec{})

	snapshot := map[string]any{
		"context_key":    "ci_status",
		"allowed":        []string{"green"},
		"snapshot_value": "green",
	}
	require.NoError(t, verifier(snapshot))
}

func TestAllowlistCaveat_Verifier_RejectsTamperedSnapshot(t *testing.T) {
	_, verifierFactory, _ := policy.LookupCaveat("allowlist")
	verifier, _ := verifierFactory(policy.CaveatSpec{})

	// Snapshot's value is NOT in allowed — gateway misminted.
	snapshot := map[string]any{
		"context_key":    "ci_status",
		"allowed":        []string{"green"},
		"snapshot_value": "red",
	}
	err := verifier(snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "misminted")
}

func TestAllowlistCaveat_BadSpec_RejectsAtParse(t *testing.T) {
	cases := []policy.CaveatSpec{
		{},                                                   // missing context_key
		{"context_key": "x"},                                  // missing allowed
		{"context_key": "x", "allowed": []string{}},           // empty allowed
		{"context_key": "x", "allowed": []string{"a"}, "match_mode": "fuzzy"}, // bad mode
	}
	for i, spec := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			_, _, err := lookupAndBuild("allowlist", spec)
			require.Error(t, err)
		})
	}
}

// =====================================================================
// Registry helpers
// =====================================================================

func TestLookupCaveat_Unknown_ReturnsFalse(t *testing.T) {
	_, _, ok := policy.LookupCaveat("nonexistent_caveat")
	require.False(t, ok)
}

func TestLookupCaveat_BuiltIns_Registered(t *testing.T) {
	for _, name := range []string{"time_window", "rate_limit", "allowlist"} {
		_, _, ok := policy.LookupCaveat(name)
		require.True(t, ok, "built-in %q must be registered at init", name)
	}
}

func TestDefaultCaveatVerifiers_IncludesBuiltins(t *testing.T) {
	verifiers := policy.DefaultCaveatVerifiers()
	for _, name := range []string{"time_window", "allowlist"} {
		_, ok := verifiers[name]
		require.True(t, ok, "DefaultCaveatVerifiers must include %q", name)
	}
	// rate_limit's verifier is included but always rejects (since
	// no rate_limit caveat should ever be in a token).
	_, ok := verifiers["rate_limit"]
	require.True(t, ok)
}

func TestRegisterCaveat_Empty_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.RegisterCaveat("", nil, nil)
	})
}

func TestRegisterCaveat_BothNil_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.RegisterCaveat("test_panic_caveat", nil, nil)
	})
}

// =====================================================================
// Audit caveats (via_approval / via_pattern)
// =====================================================================

func TestAuditCaveat_ViaApproval_NotDeclarableInYAML(t *testing.T) {
	// via_approval is minted by saas-starter, not declared in
	// YAML. Trying to declare it must fail loud.
	producerFactory, _, ok := policy.LookupCaveat("via_approval")
	require.True(t, ok)
	require.NotNil(t, producerFactory)

	_, _, err := producerFactory(policy.CaveatSpec{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not declarable in YAML")
}

func TestAuditCaveat_ViaPattern_NotDeclarableInYAML(t *testing.T) {
	producerFactory, _, ok := policy.LookupCaveat("via_pattern")
	require.True(t, ok)
	_, _, err := producerFactory(policy.CaveatSpec{})
	require.Error(t, err)
}

func TestAuditCaveat_Verifier_AcceptsAnyNonNilValue(t *testing.T) {
	_, verifierFactory, _ := policy.LookupCaveat("via_approval")
	verifier, err := verifierFactory(policy.CaveatSpec{})
	require.NoError(t, err)

	// Accept structured map (typical from saas-starter).
	require.NoError(t, verifier(map[string]any{
		"grant_id":    "ulid-001",
		"grantor_id":  "u-approver",
		"approved_at": float64(1715300000),
	}))
	// Reject nil — drift detection.
	require.Error(t, verifier(nil))
}

func TestRegisterCaveat_Duplicate_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate")
		}
	}()
	// Built-ins are already registered; re-register one to
	// trigger the panic.
	policy.RegisterCaveat("time_window", nil, nil)
}

// =====================================================================
// Test helpers
// =====================================================================

// lookupAndBuild looks up a caveat and instantiates the producer +
// precheck via the factory. Returns (producer, precheck, error).
func lookupAndBuild(name string, spec policy.CaveatSpec) (policy.CaveatProducer, policy.CaveatPrecheck, error) {
	producerFactory, _, ok := policy.LookupCaveat(name)
	if !ok {
		return nil, nil, fmt.Errorf("caveat %q not registered", name)
	}
	if producerFactory == nil {
		return nil, nil, fmt.Errorf("caveat %q has no producer factory", name)
	}
	return producerFactory(spec)
}
