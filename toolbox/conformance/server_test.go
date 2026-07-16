package conformance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/conformance"
	"github.com/codefly-dev/core/toolbox/policyguard"
)

func call(t *testing.T, server toolboxv0.ToolboxServer, name string) *toolboxv0.CallToolResponse {
	t.Helper()
	response, err := server.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: name})
	require.NoError(t, err)
	return response
}

func structured(t *testing.T, response *toolboxv0.CallToolResponse) map[string]any {
	t.Helper()
	require.Empty(t, response.Error)
	require.Len(t, response.Content, 1)
	require.NotNil(t, response.Content[0].GetStructured())
	return response.Content[0].GetStructured().AsMap()
}

func TestFixtureDiscoveryDescriptionAndStructuredResult(t *testing.T) {
	server := conformance.New(conformance.FixtureVersion)

	identity, err := server.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, conformance.FixtureName, identity.Name)
	require.Equal(t, conformance.FixtureVersion, identity.Version)
	require.NotEmpty(t, identity.Description)

	summaries, err := server.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{})
	require.NoError(t, err)
	require.Len(t, summaries.Tools, 6)
	for _, summary := range summaries.Tools {
		require.NotEmpty(t, summary.Name)
		require.NotEmpty(t, summary.Description)
	}

	description, err := server.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)
	require.Empty(t, description.Error)
	require.Equal(t, conformance.IdentityTool, description.Tool.Name)
	require.NotEmpty(t, description.Tool.Description)
	require.NotNil(t, description.Tool.InputSchema)
	require.NotNil(t, description.Tool.OutputSchema)

	result := structured(t, call(t, server, conformance.IdentityTool))
	require.Equal(t, conformance.ContractVersion, result["contract"])
	require.Equal(t, "codefly", result["subject"])
	require.Equal(t, map[string]any{
		"name": conformance.FixtureName, "version": conformance.FixtureVersion,
	}, result["fixture"])
}

func TestFixtureDeterministicError(t *testing.T) {
	server := conformance.New(conformance.FixtureVersion)
	first := call(t, server, conformance.DeterministicErrorTool)
	second := call(t, server, conformance.DeterministicErrorTool)
	require.Equal(t, conformance.DeterministicError, first.Error)
	require.Equal(t, first.Error, second.Error)
}

func TestFixtureAllowAndDenyPoliciesMakeEffectObservable(t *testing.T) {
	deniedServer := conformance.New(conformance.FixtureVersion)
	denied := policyguard.New(deniedServer, policy.DenyAllPDP{}, conformance.FixtureName)
	denial := call(t, denied, conformance.EffectIncrementTool)
	require.Contains(t, denial.Error, "deny-all")
	require.Equal(t, float64(0), structured(t, call(t, deniedServer, conformance.EffectCountTool))["count"],
		"a denied call must never reach the effect handler")

	allowedServer := conformance.New(conformance.FixtureVersion)
	allowed := policyguard.New(allowedServer, policy.AllowAllPDP{}, conformance.FixtureName)
	require.Equal(t, float64(1), structured(t, call(t, allowed, conformance.EffectIncrementTool))["count"])
	require.Equal(t, float64(1), structured(t, call(t, allowedServer, conformance.EffectCountTool))["count"])
}

func TestFixtureManifestIsProductionAdmissibleAndMatchesCatalog(t *testing.T) {
	manifest, err := resources.LoadToolboxFromDir(context.Background(), "testdata")
	require.NoError(t, err)
	require.NoError(t, manifest.ValidateForProduction())

	server := conformance.New(manifest.Version)
	names := make([]string, 0, len(server.Tools()))
	for _, tool := range server.Tools() {
		names = append(names, tool.Name)
	}
	require.NoError(t, manifest.ValidateToolCatalog(names...))

	manifest.Permissions.Required = manifest.Permissions.Required[:len(manifest.Permissions.Required)-1]
	require.ErrorContains(t, manifest.ValidateToolCatalog(names...), conformance.CrashTool,
		"binary/manifest permission drift must fail before the first invocation")
}
