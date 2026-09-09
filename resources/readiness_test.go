package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

func loadReadinessService(t *testing.T, dir, module string) (*resources.Service, []*basev0.Endpoint) {
	t.Helper()
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, filepath.Join("testdata/readiness", dir))
	require.NoError(t, err)
	service.WithModule(module)
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	return service, endpoints
}

func endpointNamed(t *testing.T, endpoints []*basev0.Endpoint, name string) *basev0.Endpoint {
	t.Helper()
	for _, endpoint := range endpoints {
		if endpoint.Name == name {
			return endpoint
		}
	}
	t.Fatalf("endpoint %q not found", name)
	return nil
}

func TestLegacyEndpointsKeepTransportOnlyReadiness(t *testing.T) {
	_, endpoints := loadReadinessService(t, "customized", "saas")
	require.Len(t, endpoints, 2)

	for _, name := range []string{standards.GRPC, standards.TCP} {
		endpoint := endpointNamed(t, endpoints, name)
		require.Nil(t, endpoint.Health, "a service that declares no health must not gain one")

		probe, declared := resources.EndpointReadinessProbe(endpoint)
		require.False(t, declared)
		require.Equal(t, basev0.ProbeKind_PROBE_KIND_TRANSPORT, resources.ProbeKindOf(probe))
	}
}

func TestGeneratedEndpointsDeclareRicherPredicates(t *testing.T) {
	_, endpoints := loadReadinessService(t, "generated", "saas")

	grpc := endpointNamed(t, endpoints, standards.GRPC)
	readiness, declared := resources.EndpointReadinessProbe(grpc)
	require.True(t, declared)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_GRPC_HEALTH, resources.ProbeKindOf(readiness))
	require.Equal(t, "accounts.v1.Accounts", readiness.GetGrpcHealth().GetService())

	plan := resources.PlanEndpointProbes(grpc)
	require.True(t, plan.ReadinessDeclared)
	require.Equal(t, `grpc health SERVING for "accounts.v1.Accounts"`, resources.DescribeProbe(plan.Readiness))
	require.Equal(t, "grpc health SERVING", resources.DescribeProbe(plan.Liveness), "liveness is its own predicate")
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_TRANSPORT, resources.ProbeKindOf(plan.Startup))
	require.Equal(t, uint32(30), plan.Startup.GetTiming().GetFailureThreshold())
	require.Equal(t, "1s", plan.Startup.GetTiming().GetInitialDelay().AsDuration().String())

	http := endpointNamed(t, endpoints, standards.HTTP)
	httpReadiness, declared := resources.EndpointReadinessProbe(http)
	require.True(t, declared)
	require.Equal(t, "/healthz", httpReadiness.GetHttp().GetPath())
	require.Equal(t, `http GET /healthz status in 200,204-299 and body containing "ok"`, resources.DescribeProbe(httpReadiness))

	admin := endpointNamed(t, endpoints, "admin")
	adminReadiness, declared := resources.EndpointReadinessProbe(admin)
	require.True(t, declared)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_AGENT, resources.ProbeKindOf(adminReadiness))

	// Liveness and startup are absent unless declared: a restart policy is never
	// inferred from a readiness declaration.
	adminPlan := resources.PlanEndpointProbes(admin)
	require.Nil(t, adminPlan.Liveness)
	require.Nil(t, adminPlan.Startup)
}

func TestHTTPProbeRejectsFailureStatuses(t *testing.T) {
	_, endpoints := loadReadinessService(t, "generated", "saas")
	probe, _ := resources.EndpointReadinessProbe(endpointNamed(t, endpoints, standards.HTTP))

	require.True(t, resources.HTTPStatusAccepted(probe.GetHttp(), 200))
	require.True(t, resources.HTTPStatusAccepted(probe.GetHttp(), 204))
	require.False(t, resources.HTTPStatusAccepted(probe.GetHttp(), 503))
	require.False(t, resources.HTTPStatusAccepted(probe.GetHttp(), 404))

	// An HTTP probe that declares no statuses still refuses 503: the audit case
	// where any reachable HTTP server was accepted.
	bare := &basev0.HttpProbe{Path: "/healthz"}
	require.True(t, resources.HTTPStatusAccepted(bare, 200))
	require.False(t, resources.HTTPStatusAccepted(bare, 503))
}

func TestHealthRoundTripsThroughDiskAndProto(t *testing.T) {
	ctx := context.Background()
	service, endpoints := loadReadinessService(t, "generated", "saas")

	dir := t.TempDir()
	require.NoError(t, service.SaveAtDir(ctx, dir))
	written, err := os.ReadFile(filepath.Join(dir, resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(written), "grpc-health")

	reloaded, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	require.Len(t, reloaded.Endpoints, len(service.Endpoints))
	for i, endpoint := range service.Endpoints {
		require.Equal(t, endpoint.Name, reloaded.Endpoints[i].Name)
		require.Equal(t, endpoint.Health, reloaded.Endpoints[i].Health)
	}

	for _, endpoint := range endpoints {
		require.Equal(t, resources.EndpointFromProto(endpoint).Health, resources.HealthFromProto(endpoint.Health))
	}
}

func TestEndpointHashSeparatesHealthDeclarations(t *testing.T) {
	ctx := context.Background()
	_, generated := loadReadinessService(t, "generated", "saas")
	grpc := endpointNamed(t, generated, standards.GRPC)

	declared, err := resources.EndpointHash(ctx, grpc)
	require.NoError(t, err)

	stripped := resources.Light(grpc)
	stripped.Health = nil
	legacy, err := resources.EndpointHash(ctx, stripped)
	require.NoError(t, err)
	require.NotEqual(t, declared, legacy)
}

func TestPlanReadinessCoversEveryRequiredEndpoint(t *testing.T) {
	consumer, err := resources.LoadServiceFromDir(context.Background(), "testdata/readiness/consumer")
	require.NoError(t, err)

	_, accounts := loadReadinessService(t, "generated", "saas")
	_, payments := loadReadinessService(t, "customized", "saas")

	requirements, err := resources.PlanReadiness(consumer.ServiceDependencies, append(accounts, payments...))
	require.NoError(t, err)

	got := map[string]string{}
	for _, requirement := range requirements {
		got[requirement.String()] = requirement.Predicate()
	}
	require.Len(t, got, 5)

	require.Contains(t, got, `saas/accounts/grpc: grpc health SERVING for "accounts.v1.Accounts"`)
	require.Contains(t, got, `saas/accounts/http: http GET /healthz status in 200,204-299 and body containing "ok"`)
	// required: false keeps the admin endpoint consumed but out of readiness —
	// an open admin port must never stand in for a closed gRPC endpoint.
	for key := range got {
		require.NotContains(t, key, "admin")
	}
	// The customized service declares nothing, so its predicate stays transport
	// and says so.
	require.Contains(t, got, "saas/payments/grpc: transport connect (legacy: endpoint declares no readiness)")
	require.Contains(t, got, "saas/payments/tcp: transport connect (legacy: endpoint declares no readiness)")
	// An endpointless dependency declared "completed" is not ready while running.
	require.Contains(t, got, "saas/seeder: successful completion")
}

func TestPlanReadinessEndpointlessDependencyDefaultsToLifecycle(t *testing.T) {
	dependency := &resources.ServiceDependency{Name: "seeder", Module: "saas"}
	require.Equal(t, resources.DependencyReadinessStarted, dependency.ReadinessMode())

	requirements, err := resources.PlanServiceDependencyReadiness(dependency, nil)
	require.NoError(t, err)
	require.Len(t, requirements, 1)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_AGENT, resources.ProbeKindOf(requirements[0].Probe))
	require.Empty(t, requirements[0].Endpoint)
}

func TestJobDependenciesRequireCompletionByDefault(t *testing.T) {
	ctx := context.Background()
	job, err := resources.LoadJobFromDir(ctx, "testdata/readiness/migrations")
	require.NoError(t, err)
	require.Len(t, job.JobDependencies, 2)

	seed, err := resources.PlanJobDependencyReadiness(job.JobDependencies[0])
	require.NoError(t, err)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_COMPLETION, resources.ProbeKindOf(seed.Probe))
	require.Equal(t, "saas/seed: successful completion", seed.String())

	watcher, err := resources.PlanJobDependencyReadiness(job.JobDependencies[1])
	require.NoError(t, err)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_AGENT, resources.ProbeKindOf(watcher.Probe))

	dir := t.TempDir()
	require.NoError(t, job.SaveToDir(ctx, dir))
	reloaded, err := resources.LoadJobFromDir(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, job.JobDependencies, reloaded.JobDependencies)
}

func TestInvalidHealthDeclarationsAreRejectedAtLoad(t *testing.T) {
	for name, tc := range map[string]struct {
		health  string
		message string
	}{
		"unknown kind": {
			health:  "readiness:\n        kind: ping",
			message: `unsupported probe kind "ping"`,
		},
		"grpc health on http endpoint": {
			health:  "readiness:\n        kind: grpc-health",
			message: `probe kind "grpc-health" requires a grpc endpoint, not http`,
		},
		"completion on an endpoint": {
			health:  "readiness:\n        kind: completion",
			message: `probe kind "completion" is only valid for a dependency that exposes no endpoint`,
		},
		"http probe without a path": {
			health:  "readiness:\n        kind: http",
			message: `probe kind "http" needs a path; there is no default health path`,
		},
		"http probe with a relative path": {
			health:  "readiness:\n        kind: http\n        path: healthz",
			message: `probe path "healthz" must start with "/"`,
		},
		"http probe with an unroutable status": {
			health:  "readiness:\n        kind: http\n        path: /healthz\n        statuses: [\"600\"]",
			message: `status "600" is outside the HTTP status range 100-599`,
		},
		"http probe with an inverted range": {
			health:  "readiness:\n        kind: http\n        path: /healthz\n        statuses: [\"299-200\"]",
			message: `status range "299-200" is inverted`,
		},
		"field that belongs to another kind": {
			health:  "readiness:\n        kind: http\n        path: /healthz\n        service: acme.v1.Health",
			message: `probe kind "http" does not accept service`,
		},
		"unparsable timing": {
			health:  "liveness:\n        kind: transport\n        period: soon",
			message: `period "soon" is not a duration`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := "kind: service\nname: billing\nversion: 0.0.1\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nendpoints:\n  - name: http\n    health:\n      " + tc.health + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ServiceConfigurationName), []byte(manifest), 0o600))

			_, err := resources.LoadServiceFromDir(context.Background(), dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.message)
			require.Contains(t, err.Error(), `endpoint "http" health`)
		})
	}
}

func TestUnsupportedDependencyReadinessIsRejectedAtLoad(t *testing.T) {
	dir := t.TempDir()
	manifest := "kind: service\nname: gateway\nversion: 0.0.1\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nservice-dependencies:\n  - name: seeder\n    module: saas\n    readiness: eventually\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ServiceConfigurationName), []byte(manifest), 0o600))

	_, err := resources.LoadServiceFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), `dependency saas/seeder declares unsupported readiness "eventually" (expected one of started, completed)`)
}

func TestPlanReadinessRejectsContradictoryDeclarations(t *testing.T) {
	_, accounts := loadReadinessService(t, "generated", "saas")

	completedButServing := &resources.ServiceDependency{
		Name:      "accounts",
		Module:    "saas",
		Readiness: resources.DependencyReadinessCompleted,
		Endpoints: []*resources.EndpointReference{{Name: standards.GRPC}},
	}
	_, err := resources.PlanServiceDependencyReadiness(completedButServing, accounts)
	require.ErrorContains(t, err, `dependency saas/accounts declares readiness "completed" but requires endpoint grpc`)

	missing := &resources.ServiceDependency{
		Name:      "accounts",
		Module:    "saas",
		Endpoints: []*resources.EndpointReference{{Name: "metrics"}},
	}
	require.ErrorContains(t, resources.ValidateReadiness([]*resources.ServiceDependency{missing}, accounts),
		`service dependency saas/accounts declares undeclared endpoint "metrics"`)
}

func TestFailingPredicatesNameTheFailingCheck(t *testing.T) {
	_, accounts := loadReadinessService(t, "generated", "saas")
	dependency := &resources.ServiceDependency{
		Name:      "accounts",
		Module:    "saas",
		Endpoints: []*resources.EndpointReference{{Name: standards.GRPC}},
	}
	requirements, err := resources.PlanServiceDependencyReadiness(dependency, accounts)
	require.NoError(t, err)

	report := &basev0.HealthReport{Generation: 3}
	report.Results = append(report.Results,
		requirements[0].Result(basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, "connection refused"))

	result := report.Results[0]
	require.Equal(t, basev0.ProbeIntent_PROBE_INTENT_READINESS, result.Intent)
	require.Equal(t, basev0.ProbeKind_PROBE_KIND_GRPC_HEALTH, result.Kind)
	require.Equal(t, "saas/accounts", result.Dependency)
	require.Equal(t, standards.GRPC, result.Api)
	require.Equal(t, []string{`saas/accounts/grpc: grpc health SERVING for "accounts.v1.Accounts" (connection refused)`},
		resources.FailingPredicates(report))
}
