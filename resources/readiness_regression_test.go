package resources_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

// "The producer exposes nothing" and "I opted out of every endpoint" are
// different declarations; collapsing them silently gated on a lifecycle the
// consumer never asked for.
func TestOptingOutOfEveryEndpointIsNotSilentlyALifecycleCheck(t *testing.T) {
	_, endpoints := loadReadinessService(t, "generated", "saas")
	no := false

	optedOut := func(readiness resources.DependencyReadiness) *resources.ServiceDependency {
		return &resources.ServiceDependency{
			Name: "accounts", Module: "saas", Readiness: readiness,
			Endpoints: []*resources.EndpointReference{{Name: standards.GRPC, Required: &no}},
		}
	}

	_, err := resources.PlanServiceDependencyReadiness(optedOut(""), endpoints)
	require.ErrorContains(t, err, "consumes 1 endpoint(s) but requires none for readiness")
	require.ErrorContains(t, err, `declare readiness "started"`)
	require.ErrorContains(t, err, `or "ignore"`)

	// Both intents are now expressible, and each says which it is.
	gated, err := resources.PlanServiceDependencyReadiness(optedOut(resources.DependencyReadinessStarted), endpoints)
	require.NoError(t, err)
	require.Len(t, gated, 1)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_AGENT, resources.ProbeKindOf(gated[0].Probe))

	ignored, err := resources.PlanServiceDependencyReadiness(optedOut(resources.DependencyReadinessIgnore), endpoints)
	require.NoError(t, err)
	require.Empty(t, ignored)

	contradictory := &resources.ServiceDependency{
		Name: "accounts", Module: "saas", Readiness: resources.DependencyReadinessIgnore,
		Endpoints: []*resources.EndpointReference{{Name: standards.GRPC}},
	}
	_, err = resources.PlanServiceDependencyReadiness(contradictory, endpoints)
	require.ErrorContains(t, err, `declares readiness "ignore" but requires endpoint grpc`)
}

// An agent can return any Endpoint from Load. A contradiction used to be
// accepted at ingestion and only fail later, when the manifest was written back.
func TestWireEndpointsAreValidatedAtIngestion(t *testing.T) {
	grpcHealthOnHTTP := &basev0.Endpoint{
		Name: standards.HTTP, Service: "accounts", Module: "saas", Api: standards.HTTP,
		Health: &basev0.Health{Readiness: &basev0.Probe{
			Predicate: &basev0.Probe_GrpcHealth{GrpcHealth: &basev0.GrpcHealthProbe{}},
		}},
	}
	_, err := resources.FromProtoEndpoints(grpcHealthOnHTTP)
	require.ErrorContains(t, err, `endpoint "http" health: readiness: probe kind "grpc-health" requires a grpc endpoint, not http`)
	require.Error(t, resources.ValidateEndpointHealth(grpcHealthOnHTTP))

	// "Declared nothing" and "declared something empty" stay distinct: the first
	// is legacy transport, the second is an unusable probe.
	empty := &basev0.Endpoint{
		Name: standards.HTTP, Service: "accounts", Module: "saas", Api: standards.HTTP,
		Health: &basev0.Health{Readiness: &basev0.Probe{}},
	}
	require.ErrorContains(t, resources.ValidateEndpointHealth(empty), `readiness declares no predicate`)

	valid := &basev0.Endpoint{
		Name: standards.GRPC, Service: "accounts", Module: "saas", Api: standards.GRPC,
		Health: &basev0.Health{Readiness: &basev0.Probe{
			Predicate: &basev0.Probe_GrpcHealth{GrpcHealth: &basev0.GrpcHealthProbe{Service: "accounts.v1.Accounts"}},
		}},
	}
	converted, err := resources.FromProtoEndpoints(valid)
	require.NoError(t, err)
	require.Equal(t, resources.ProbeKindGRPCHealth, converted[0].Health.Readiness.Kind)
}

// The transport security an evaluator needs is recorded only in the endpoint's
// API details, so the plan has to carry it or a TLS endpoint is dialled plain.
func TestPlanCarriesEndpointTransportSecurity(t *testing.T) {
	secured := &basev0.Endpoint{
		Name: standards.GRPC, Service: "accounts", Module: "saas", Api: standards.GRPC,
		ApiDetails: &basev0.API{Value: &basev0.API_Grpc{Grpc: &basev0.GrpcAPI{Secured: true}}},
	}
	plain := &basev0.Endpoint{
		Name: standards.HTTP, Service: "accounts", Module: "saas", Api: standards.HTTP,
		ApiDetails: &basev0.API{Value: &basev0.API_Http{Http: &basev0.HttpAPI{}}},
	}
	require.True(t, resources.EndpointSecured(secured))
	require.False(t, resources.EndpointSecured(plain))

	dependency := &resources.ServiceDependency{Name: "accounts", Module: "saas"}
	requirements, err := resources.PlanReadiness([]*resources.ServiceDependency{dependency}, []*basev0.Endpoint{secured, plain})
	require.NoError(t, err)
	require.Len(t, requirements, 2)

	bySecurity := map[string]bool{}
	for _, requirement := range requirements {
		bySecurity[requirement.Endpoint] = requirement.Secured
	}
	require.True(t, bySecurity[standards.GRPC])
	require.False(t, bySecurity[standards.HTTP])
}

// The unsupported-kind error used to recommend "completion", which the next
// validation step then rejected for any endpoint.
func TestUnsupportedKindErrorOffersOnlyKindsAnEndpointAccepts(t *testing.T) {
	_, err := (&resources.Probe{Kind: "ping"}).Proto(standards.HTTP)
	require.ErrorContains(t, err, "expected one of transport, grpc-health, http, agent")
	require.NotContains(t, err.Error(), resources.ProbeKindCompletion)

	_, err = (&resources.Probe{}).Proto(standards.HTTP)
	require.NotContains(t, err.Error(), resources.ProbeKindCompletion)
}

// AcceptedHTTPStatuses handed callers the package's own default slice.
func TestDefaultHTTPStatusesCannotBeMutatedByCallers(t *testing.T) {
	probe := &basev0.HttpProbe{Path: "/healthz"}
	resources.AcceptedHTTPStatuses(probe)[0].Max = 599

	require.False(t, resources.HTTPStatusAccepted(probe, 503), "a caller must not be able to widen the default for every probe")
	require.True(t, resources.HTTPStatusAccepted(probe, 200))
}

// Adding the health field must not disturb the hash of an endpoint that
// declares none, or consumers keyed off it re-run everything on upgrade.
func TestEndpointHashIsStableForEndpointsWithoutHealth(t *testing.T) {
	ctx := context.Background()
	_, endpoints := loadReadinessService(t, "customized", "saas")

	first, err := resources.EndpointHash(ctx, endpoints...)
	require.NoError(t, err)
	second, err := resources.EndpointHash(ctx, endpoints...)
	require.NoError(t, err)
	require.Equal(t, first, second)

	withHealth := resources.Light(endpoints[0])
	withHealth.Health = &basev0.Health{Readiness: &basev0.Probe{
		Predicate: &basev0.Probe_GrpcHealth{GrpcHealth: &basev0.GrpcHealthProbe{}},
	}}
	changed, err := resources.EndpointHash(ctx, withHealth)
	require.NoError(t, err)
	stable, err := resources.EndpointHash(ctx, withHealth)
	require.NoError(t, err)
	require.Equal(t, changed, stable, "the health contribution must be deterministic")
}
