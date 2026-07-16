package policy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func fullWellKnownCaveats(t *testing.T) map[string]any {
	t.Helper()
	caveats, err := (policy.WellKnownCaveats{
		OrganizationID:  "org-1",
		TenantID:        "tenant-1",
		Environment:     "production",
		ResourceBinding: "database:tenant-1",
		QueryIDs:        []string{"customers.list", "customers.get"},
		ResultBudget: &policy.ResultBudget{
			MaxRows: 100, MaxBytes: 1_000_000, MaxDurationMillis: 5_000,
		},
		ReleaseID:  "release-1",
		ApprovalID: "approval-1",
	}).Map()
	require.NoError(t, err)
	return caveats
}

func fullWellKnownExpectations() policy.WellKnownCaveatExpectations {
	return policy.WellKnownCaveatExpectations{
		OrganizationID:  "org-1",
		TenantID:        "tenant-1",
		Environment:     "production",
		ResourceBinding: "database:tenant-1",
		QueryID:         "customers.list",
		ResultBudget: &policy.ResultBudget{
			MaxRows: 50, MaxBytes: 500_000, MaxDurationMillis: 2_000,
		},
		ReleaseID:  "release-1",
		ApprovalID: "approval-1",
	}
}

func verifyWellKnown(t *testing.T, caveats map[string]any, expectations policy.WellKnownCaveatExpectations) error {
	t.Helper()
	secret := policy.NewSpawnSecret()
	token, _, err := policy.Mint(policy.MintInput{
		Principal: defaultPrincipal(),
		Action:    "postgres.query.execute",
		TTL:       time.Minute,
		Caveats:   caveats,
	}, secret)
	require.NoError(t, err)
	verification, err := policy.NewWellKnownCaveatVerification(expectations)
	require.NoError(t, err)
	_, err = policy.Verify(token, policy.VerifyExpectations{
		Action:          "postgres.query.execute",
		CaveatVerifiers: verification.Verifiers,
		RequiredCaveats: verification.Required,
	}, secret)
	return err
}

func TestWellKnownCaveats_AllBindingsVerify(t *testing.T) {
	require.NoError(t, verifyWellKnown(t, fullWellKnownCaveats(t), fullWellKnownExpectations()))
}

func TestWellKnownCaveats_WrongBindingsDeny(t *testing.T) {
	tests := []struct {
		name string
		edit func(*policy.WellKnownCaveatExpectations)
	}{
		{name: "organization", edit: func(e *policy.WellKnownCaveatExpectations) { e.OrganizationID = "org-2" }},
		{name: "tenant", edit: func(e *policy.WellKnownCaveatExpectations) { e.TenantID = "tenant-2" }},
		{name: "environment", edit: func(e *policy.WellKnownCaveatExpectations) { e.Environment = "staging" }},
		{name: "resource", edit: func(e *policy.WellKnownCaveatExpectations) { e.ResourceBinding = "database:tenant-2" }},
		{name: "query", edit: func(e *policy.WellKnownCaveatExpectations) { e.QueryID = "customers.delete" }},
		{name: "release", edit: func(e *policy.WellKnownCaveatExpectations) { e.ReleaseID = "release-2" }},
		{name: "approval", edit: func(e *policy.WellKnownCaveatExpectations) { e.ApprovalID = "approval-2" }},
		{name: "row budget", edit: func(e *policy.WellKnownCaveatExpectations) { e.ResultBudget.MaxRows = 101 }},
		{name: "byte budget", edit: func(e *policy.WellKnownCaveatExpectations) { e.ResultBudget.MaxBytes = 1_000_001 }},
		{name: "time budget", edit: func(e *policy.WellKnownCaveatExpectations) { e.ResultBudget.MaxDurationMillis = 5_001 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expect := fullWellKnownExpectations()
			tc.edit(&expect)
			err := verifyWellKnown(t, fullWellKnownCaveats(t), expect)
			require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
		})
	}
}

func TestWellKnownCaveats_ExpectedButMissingDenies(t *testing.T) {
	caveats := fullWellKnownCaveats(t)
	delete(caveats, string(policy.CaveatTenantID))
	err := verifyWellKnown(t, caveats, fullWellKnownExpectations())
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "required caveat \"tenant_id\" is missing")
}

func TestWellKnownCaveats_InvalidTypedValuesRejectBeforeMint(t *testing.T) {
	_, err := (policy.WellKnownCaveats{QueryIDs: []string{"q", "q"}}).Map()
	require.ErrorContains(t, err, "duplicates")

	_, err = (policy.WellKnownCaveats{ResultBudget: &policy.ResultBudget{
		MaxRows: 0, MaxBytes: 10, MaxDurationMillis: 10,
	}}).Map()
	require.ErrorContains(t, err, "max_rows")

	_, err = policy.NewWellKnownCaveatVerification(policy.WellKnownCaveatExpectations{TenantID: " tenant"})
	require.ErrorContains(t, err, "whitespace")
}

func TestProviderCaveat_RequiresExplicitVerifier(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token, _, err := policy.Mint(policy.MintInput{
		Principal: defaultPrincipal(), Action: "x.y", TTL: time.Minute,
		Caveats: map[string]any{"acme/region_lock": "us-east-1"},
	}, secret)
	require.NoError(t, err)

	_, err = policy.Verify(token, policy.VerifyExpectations{Action: "x.y"}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
	require.ErrorContains(t, err, "unknown caveat")

	_, err = policy.Verify(token, policy.VerifyExpectations{
		Action: "x.y",
		CaveatVerifiers: map[string]policy.CaveatVerifier{
			"acme/region_lock": func(value any) error {
				if value != "us-east-1" {
					return errors.New("wrong region")
				}
				return nil
			},
		},
		RequiredCaveats: []string{"acme/region_lock"},
	}, secret)
	require.NoError(t, err)
}

func TestRequiredCaveatWithoutVerifierDeniesConfiguration(t *testing.T) {
	secret := policy.NewSpawnSecret()
	token, _, err := policy.Mint(policy.MintInput{
		Principal: defaultPrincipal(), Action: "x.y", TTL: time.Minute,
		Caveats: map[string]any{"tenant_id": "tenant-1"},
	}, secret)
	require.NoError(t, err)
	_, err = policy.Verify(token, policy.VerifyExpectations{
		Action:          "x.y",
		RequiredCaveats: []string{"tenant_id"},
	}, secret)
	require.ErrorIs(t, err, policy.ErrScopedAuthInvalid)
}
