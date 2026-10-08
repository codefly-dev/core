package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/standards"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

	"github.com/stretchr/testify/require"
)

func TestREST(t *testing.T) {
	ctx := context.Background()
	rest, err := resources.LoadRestAPI(ctx, shared.Pointer("testdata/endpoints/basic/openapi/api.json"))
	require.NoError(t, err)
	require.Equal(t, 2, len(rest.Groups)) // 2 Paths
	var routes []*basev0.RestRoute
	for _, group := range rest.Groups {
		routes = append(routes, group.Routes...)
	}
	require.Equal(t, 3, len(routes)) // 3 Routes (1 path with 2 Methods)
}

func TestGRPC(t *testing.T) {
	ctx := context.Background()
	grpc, err := resources.LoadGrpcAPI(ctx, shared.Pointer("testdata/endpoints/basic/proto/api.proto"))
	require.NoError(t, err)
	require.Equal(t, "management.organization", grpc.Package)
	require.Equal(t, 4, len(grpc.Rpcs)) // 4 RPCs
	for _, rpc := range grpc.Rpcs {
		require.Equal(t, "OrganizationService", rpc.ServiceName)
	}
}

// NewAPI must carry every axis of the endpoint's declaration, not reach alone.
// It used to copy Visibility and drop Exposure and Location, so an agent that
// materialized a PUBLIC endpoint here got one with no exposure — a declaration
// this package's own ValidateEndpointDeclaration refuses. The refusal then
// named the module's manifest, which did declare it, so the loss was invisible
// at the place it happened. Light() always carried all three; this did not.
func TestNewAPICarriesEveryAxisOfTheDeclaration(t *testing.T) {
	for _, declared := range []*resources.Endpoint{
		{Module: "m", Service: "s", Name: "rest", API: standards.REST,
			Visibility: resources.VisibilityPublic, Exposure: resources.ExposurePublic},
		{Module: "m", Service: "s", Name: "rest", API: standards.REST,
			Visibility: resources.VisibilityPublic, Exposure: resources.ExposureNone},
		{Module: "m", Service: "s", Name: "rest", API: standards.REST,
			Visibility: resources.VisibilityPublic, Exposure: resources.ExposureNone,
			Location: resources.LocationExternal},
		{Module: "m", Service: "s", Name: "grpc", API: standards.GRPC,
			Visibility: resources.VisibilityInternal},
	} {
		produced, err := resources.NewAPI(context.Background(), declared, nil)
		require.NoError(t, err)
		require.Equal(t, declared.Visibility, produced.GetVisibility(), "visibility")
		require.Equal(t, declared.Exposure, produced.GetExposure(), "exposure")
		require.Equal(t, declared.Location, produced.GetLocation(), "location")
		// The point of carrying them: what comes out is loadable. A public
		// endpoint that lost its exposure here is refused by this very package.
		require.NoError(t, resources.ValidateEndpointDeclaration(resources.EndpointDeclarationOf(produced)),
			"an endpoint produced by NewAPI must be one this package accepts")
	}
}
