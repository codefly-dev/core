package solution

import (
	"testing"

	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"github.com/stretchr/testify/require"
)

func TestPolicyForResolvesByFullMethodName(t *testing.T) {
	policy, isSolution := policyFor(solutionv0.Solution_Package_FullMethodName)
	require.True(t, isSolution)
	require.Equal(t, solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_WRITE, policy.GetNetwork())
	require.Equal(t, solutionv0.SolutionEffect_SOLUTION_EFFECT_REGISTRY_WRITE, policy.GetEffect())

	_, isSolution = policyFor("/grpc.health.v1.Health/Check")
	require.False(t, isSolution)

	_, isSolution = policyFor("/codefly.services.solution.v0.Solution/DoesNotExist")
	require.False(t, isSolution)
}

func TestAdmitsEnforcesBothAxesAndFailsClosed(t *testing.T) {
	create := &solutionv0.SolutionMethodPolicy{Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE}
	pkg := &solutionv0.SolutionMethodPolicy{Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_WRITE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_REGISTRY_WRITE}

	require.NoError(t, admits(create, CeilingScaffold()))
	require.NoError(t, admits(create, CeilingPublish()))
	// Package exceeds a scaffold ceiling on both axes.
	require.Error(t, admits(pkg, CeilingScaffold()))
	require.NoError(t, admits(pkg, CeilingPublish()))

	// Fail closed: nil policy, unspecified policy field, zero-value ceiling.
	require.Error(t, admits(nil, CeilingPublish()))
	require.Error(t, admits(&solutionv0.SolutionMethodPolicy{Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_READ_ONLY}, CeilingPublish()))
	require.Error(t, admits(create, Ceiling{}))
}

// TestOperationCeilingsAdmitExactlyTheirRPCs pins the provenance chokepoint: each
// named operation ceiling admits exactly the Solution RPCs that operation is
// allowed to invoke and denies the rest. A drift in the intent→ceiling mapping
// (or an RPC's declared policy) surfaces here rather than silently widening what
// an operation can dispatch.
func TestOperationCeilingsAdmitExactlyTheirRPCs(t *testing.T) {
	rpcPolicy := func(fullMethod string) *solutionv0.SolutionMethodPolicy {
		policy, isSolution := policyFor(fullMethod)
		require.True(t, isSolution, fullMethod)
		return policy
	}
	all := map[string]*solutionv0.SolutionMethodPolicy{
		"GetSolutionInformation": rpcPolicy(solutionv0.Solution_GetSolutionInformation_FullMethodName),
		"Create":                 rpcPolicy(solutionv0.Solution_Create_FullMethodName),
		"Update":                 rpcPolicy(solutionv0.Solution_Update_FullMethodName),
		"Package":                rpcPolicy(solutionv0.Solution_Package_FullMethodName),
		"Render":                 rpcPolicy(solutionv0.Solution_Render_FullMethodName),
	}
	cases := []struct {
		name    string
		ceiling Ceiling
		admits  map[string]bool
	}{
		{"inspect", CeilingInspect(), map[string]bool{"GetSolutionInformation": true}},
		{"scaffold", CeilingScaffold(), map[string]bool{
			"GetSolutionInformation": true, "Create": true, "Update": true, "Render": true,
		}},
		{"publish", CeilingPublish(), map[string]bool{
			"GetSolutionInformation": true, "Create": true, "Update": true, "Render": true, "Package": true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for rpc, policy := range all {
				err := admits(policy, tc.ceiling)
				if tc.admits[rpc] {
					require.NoError(t, err, "%s must admit %s", tc.name, rpc)
				} else {
					require.Error(t, err, "%s must deny %s", tc.name, rpc)
				}
			}
		})
	}
}
