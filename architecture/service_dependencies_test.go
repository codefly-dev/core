package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func createServices(services ...string) []architecture.Service {
	var out []architecture.Service
	for _, service := range services {
		out = append(out, architecture.Service{Unique: service})
	}
	return out
}

// Modules Layout:
// management:
// - organization
// billing
// - accounts -> management/organization
// web:
// - gateway  -> management/organization
// - gateway  -> billing/accounts
// - frontend -> gateway

// Flat Layout:
// organization
// accounts -> organization
// gateway -> organization
// gateway -> accounts
// frontend -> gateway

func TestServiceDependenciesModulesLayout(t *testing.T) {
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/module-layout")
	require.NoError(t, err)
	require.NotNil(t, workspace)
	require.Equal(t, 3, len(workspace.Modules))

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "management/organization"))
	require.NotNil(t, organization)
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "billing/accounts"))
	require.NotNil(t, accounts)
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/gateway"))
	require.NotNil(t, gateway)
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/frontend"))
	require.NotNil(t, frontend)

	testServiceGraph(t, workspace, organization.MustUnique(), accounts.MustUnique(), gateway.MustUnique(), frontend.MustUnique())
}

func TestServiceDependenciesFlatLayout(t *testing.T) {
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "organization"))
	require.NotNil(t, organization)
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "accounts"))
	require.NotNil(t, accounts)
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway"))
	require.NotNil(t, gateway)
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "frontend"))
	require.NotNil(t, frontend)

	testServiceGraph(t, workspace, organization.MustUnique(), accounts.MustUnique(), gateway.MustUnique(), frontend.MustUnique())
}

func TestServiceDependenciesFlatLayoutWithSkip(t *testing.T) {
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "organization"))
	require.NotNil(t, organization)
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "accounts"))
	require.NotNil(t, accounts)
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway"))
	require.NotNil(t, gateway)
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "frontend"))
	require.NotNil(t, frontend)

	// We want to skip dependencies on gateway

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.SkipDependencyFor(gateway.MustUnique()))
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Topological order to frontend should only be gateway -> web
	to, err := dep.OrderTo(ctx, frontend.MustUnique())
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: gateway.MustUnique()}}, to)

}

func TestServiceDependenciesFlatLayoutWithExclude(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "organization"))
	require.NotNil(t, organization)
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "accounts"))
	require.NotNil(t, accounts)
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway"))
	require.NotNil(t, gateway)
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "frontend"))
	require.NotNil(t, frontend)

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.ExcludeServices(accounts.MustUnique()))
	require.NoError(t, err)
	require.NotNil(t, dep)

	_, err = dep.ServiceFromUnique(accounts.MustUnique())
	require.Error(t, err)

	to, err := dep.OrderTo(ctx, frontend.MustUnique())
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: organization.MustUnique()}, {Unique: gateway.MustUnique()}}, to)

	depends, err := dep.DependsOn(gateway.MustUnique(), accounts.MustUnique())
	require.Error(t, err)
	require.False(t, depends)
}

func testServiceGraph(t *testing.T, workspace *resources.Workspace, organization, accounts, gateway, frontend string) {
	ctx := context.Background()
	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.NotNil(t, dep)

	for _, service := range []string{organization, accounts, gateway, frontend} {
		svc, err := dep.ServiceFromUnique(service)
		require.NoError(t, err)
		require.NotNil(t, svc)
		require.Equal(t, service, svc.MustUnique())
	}

	require.Equal(t, 4, len(dep.Services()))

	require.Equal(t, 4, len(dep.Dependencies()))

	// Sanity checks
	ok, err := dep.DependsOn(accounts, organization)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = dep.DependsOn(gateway, organization)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = dep.DependsOn(gateway, accounts)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = dep.DependsOn(frontend, organization)
	require.NoError(t, err)
	require.True(t, ok)

	// Check Restrict
	smallerDep, err := dep.Restrict(ctx, accounts)
	require.NoError(t, err)

	require.ElementsMatch(t, createServices(accounts, organization), smallerDep.Services())

	// Check DirectRequires

	deps, err := dep.DirectRequires(ctx, organization)
	require.NoError(t, err)
	require.Equal(t, 0, len(deps))

	deps, err = dep.DirectRequires(ctx, accounts)
	require.NoError(t, err)
	require.Equal(t, createServices(organization), deps)

	deps, err = dep.DirectRequires(ctx, gateway)
	require.NoError(t, err)
	require.Equal(t, createServices(organization, accounts), deps)

	deps, err = dep.DirectRequires(ctx, frontend)
	require.NoError(t, err)
	require.Equal(t, createServices(gateway), deps)

	// Check DirectDependents

	reqs, err := dep.DirectDependents(ctx, frontend)
	require.NoError(t, err)
	require.Equal(t, 0, len(reqs))

	reqs, err = dep.DirectDependents(ctx, gateway)
	require.NoError(t, err)
	require.Equal(t, createServices(frontend), reqs)

	reqs, err = dep.DirectDependents(ctx, organization)
	require.NoError(t, err)
	require.Equal(t, createServices(accounts, gateway), reqs)

	// Topological sorts

	order, err := dep.OrderTo(ctx, organization)
	require.NoError(t, err)
	require.Equal(t, 0, len(order))

	order, err = dep.OrderTo(ctx, accounts)
	require.NoError(t, err)
	expected := []architecture.Service{
		{organization},
	}
	require.Equal(t, expected, order)

	order, err = dep.OrderTo(ctx, gateway)
	require.NoError(t, err)
	expected = []architecture.Service{
		{organization},
		{accounts},
	}
	require.Equal(t, expected, order)

	order, err = dep.OrderTo(ctx, frontend)
	require.NoError(t, err)
	expected = []architecture.Service{
		{organization},
		{accounts},
		{gateway},
	}
	require.Equal(t, expected, order)

	entryPoints, err := dep.EntryPoints(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, len(entryPoints))
	require.Equal(t, frontend, entryPoints[0].Unique)
}

func requireEveryServiceResolvable(t *testing.T, dep *architecture.ServiceDependencies) {
	t.Helper()
	for _, service := range dep.Services() {
		svc, err := dep.ServiceFromUnique(service.Unique)
		require.NoErrorf(t, err, "service <%s> is in the graph and must be resolvable", service.Unique)
		require.Equal(t, service.Unique, svc.MustUnique())
	}
}

func requireNotResolvable(t *testing.T, dep *architecture.ServiceDependencies, unique string) {
	t.Helper()
	svc, err := dep.ServiceFromUnique(unique)
	require.Nil(t, svc)
	var notFound *shared.ErrorResourceNotFound
	require.ErrorAs(t, err, &notFound)
}

func TestRestrictKeepsServiceLookup(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/module-layout")
	require.NoError(t, err)

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "management/organization")).MustUnique()
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "billing/accounts")).MustUnique()
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/gateway")).MustUnique()
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/frontend")).MustUnique()

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	beforeGateway, err := dep.ServiceFromUnique(gateway)
	require.NoError(t, err)

	restricted, err := dep.Restrict(ctx, frontend)
	require.NoError(t, err)
	require.ElementsMatch(t, createServices(organization, accounts, gateway, frontend), restricted.Services())
	requireEveryServiceResolvable(t, restricted)

	afterGateway, err := restricted.ServiceFromUnique(gateway)
	require.NoError(t, err)
	require.Same(t, beforeGateway, afterGateway)

	t.Run("removed services are not found", func(t *testing.T) {
		toAccounts, err := dep.Restrict(ctx, accounts)
		require.NoError(t, err)
		require.ElementsMatch(t, createServices(organization, accounts), toAccounts.Services())
		requireEveryServiceResolvable(t, toAccounts)
		requireNotResolvable(t, toAccounts, gateway)
		requireNotResolvable(t, toAccounts, frontend)
	})

	t.Run("service without dependencies", func(t *testing.T) {
		toOrganization, err := dep.Restrict(ctx, organization)
		require.NoError(t, err)
		require.Equal(t, createServices(organization), toOrganization.Services())
		requireEveryServiceResolvable(t, toOrganization)
	})

	t.Run("repeated restriction", func(t *testing.T) {
		toGateway, err := restricted.Restrict(ctx, gateway)
		require.NoError(t, err)
		require.ElementsMatch(t, createServices(organization, accounts, gateway), toGateway.Services())
		requireEveryServiceResolvable(t, toGateway)
		requireNotResolvable(t, toGateway, frontend)
	})

	t.Run("unknown target", func(t *testing.T) {
		_, err := dep.Restrict(ctx, "web/unknown")
		require.Error(t, err)
	})

	t.Run("original is untouched", func(t *testing.T) {
		require.ElementsMatch(t, createServices(organization, accounts, gateway, frontend), dep.Services())
		require.Equal(t, 4, len(dep.Dependencies()))
		requireEveryServiceResolvable(t, dep)
	})
}

func TestRestrictKeepsDependencyOptions(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/module-layout")
	require.NoError(t, err)

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "management/organization")).MustUnique()
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "billing/accounts")).MustUnique()
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/gateway")).MustUnique()
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "web/frontend")).MustUnique()

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.ExcludeServices(accounts))
	require.NoError(t, err)

	restricted, err := dep.Restrict(ctx, frontend)
	require.NoError(t, err)

	require.ElementsMatch(t, createServices(organization, gateway, frontend), restricted.Services())
	requireEveryServiceResolvable(t, restricted)
	requireNotResolvable(t, restricted, accounts)
}
