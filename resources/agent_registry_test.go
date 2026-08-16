package resources_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestAgentKindRegistryIsExhaustiveAndFailClosed(t *testing.T) {
	require.Equal(t, int32(1), int32(basev0.Agent_SERVICE))
	require.Equal(t, int32(2), int32(basev0.Agent_JOB))
	require.Equal(t, int32(3), int32(basev0.Agent_APPLICATION))
	require.Equal(t, int32(4), int32(basev0.Agent_MODULE))
	require.Equal(t, int32(5), int32(basev0.Agent_TOOLBOX))
	require.Equal(t, int32(6), int32(basev0.Agent_PROVIDER))
	require.Equal(t, int32(7), int32(basev0.Agent_SOLUTION))
	expected := map[basev0.Agent_Kind]struct {
		resource   string
		subdir     string
		prefix     string
		resolution resources.AgentResolutionSupport
		operations resources.AgentOperationSupport
	}{
		basev0.Agent_SERVICE: {
			"codefly:service", "services", "service",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionLocalExecutable, OCI: resources.AgentResolutionBinaryDigest,
				Nix: resources.AgentResolutionBinaryDigest, GitHub: resources.AgentResolutionGitHubRelease,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		},
		basev0.Agent_JOB: {"codefly:job", "jobs", "job", resources.AgentResolutionSupport{}, resources.AgentOperationSupport{}},
		basev0.Agent_APPLICATION: {
			"codefly:application", "applications", "application",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionLocalExecutable, OCI: resources.AgentResolutionBinaryDigest,
				Nix: resources.AgentResolutionBinaryDigest, GitHub: resources.AgentResolutionGitHubRelease,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		},
		basev0.Agent_MODULE: {
			"codefly:module", "modules", "module",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionLocalExecutable, OCI: resources.AgentResolutionBinaryDigest,
				Nix: resources.AgentResolutionBinaryDigest, GitHub: resources.AgentResolutionGitHubRelease,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, Load: true},
		},
		basev0.Agent_TOOLBOX: {
			"codefly:toolbox", "toolboxes", "toolbox",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionLocalExecutable, OCI: resources.AgentResolutionBinaryDigest,
				Nix: resources.AgentResolutionBinaryDigest, GitHub: resources.AgentResolutionGitHubRelease,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		},
		basev0.Agent_PROVIDER: {
			"codefly:provider", "providers", "provider",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionVerifiedArtifact, Nix: resources.AgentResolutionVerifiedArtifact,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		},
		basev0.Agent_SOLUTION: {
			"codefly:solution", "solutions", "solution",
			resources.AgentResolutionSupport{
				Local: resources.AgentResolutionVerifiedArtifact, Nix: resources.AgentResolutionVerifiedArtifact,
			},
			resources.AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		},
	}
	registry := resources.AgentKindRegistry()
	require.Len(t, registry, len(expected))

	repositories := map[string]struct{}{}
	assets := map[string]struct{}{}
	for _, registration := range registry {
		want, ok := expected[registration.ProtoKind]
		require.True(t, ok, "unexpected proto kind %s", registration.ProtoKind)
		require.Equal(t, resources.AgentKind(want.resource), registration.Resource)
		require.Equal(t, want.subdir, registration.InstallSubdirectory)
		require.Equal(t, want.prefix, registration.ExecutablePrefix)
		require.Equal(t, want.resolution, registration.Resolution)
		require.Equal(t, want.operations, registration.Operations)
		require.Equal(t, registration.AutoDownload, registration.Resolution.GitHub == resources.AgentResolutionGitHubRelease)
		for store, behavior := range map[resources.AgentStore]resources.AgentResolutionBehavior{
			resources.AgentStoreLocal: registration.Resolution.Local, resources.AgentStoreOCI: registration.Resolution.OCI,
			resources.AgentStoreNix: registration.Resolution.Nix, resources.AgentStoreGitHub: registration.Resolution.GitHub,
		} {
			got, err := registration.Resolution.For(store)
			require.NoError(t, err)
			require.Equal(t, behavior, got)
		}
		require.NotContains(t, repositories, registration.GitHubRepository("same-name"))
		require.NotContains(t, assets, registration.GitHubAsset("same-name", "1.2.3", runtime.GOOS, runtime.GOARCH))
		repositories[registration.GitHubRepository("same-name")] = struct{}{}
		assets[registration.GitHubAsset("same-name", "1.2.3", runtime.GOOS, runtime.GOARCH)] = struct{}{}

		roundTrip, err := resources.AgentKindFromProto(registration.ProtoKind)
		require.NoError(t, err)
		require.Equal(t, registration.Resource, *roundTrip)
	}

	_, err := resources.AgentKindFromProto(basev0.Agent_UNKNOWN)
	require.ErrorContains(t, err, "unknown agent kind")
	_, err = resources.LoadAgent(context.Background(), &basev0.Agent{
		Kind: basev0.Agent_UNKNOWN, Publisher: "codefly.dev", Name: "unknown",
	}, resources.ServiceAgent)
	require.ErrorContains(t, err, "unknown agent kind")
	_, err = resources.AgentKindRegistrationFor("codefly:unknown")
	require.ErrorContains(t, err, "unknown agent kind")
}

func TestAgentRegistryRejectsIncompleteRegistration(t *testing.T) {
	err := resources.ValidateAgentKindRegistration(resources.AgentKindRegistration{
		ProtoKind: basev0.Agent_PROVIDER,
		Resource:  resources.ProviderAgent,
		Operations: resources.AgentOperationSupport{
			Load: true,
		},
	})
	require.Error(t, err)
}

func TestAgentPathsUseRegisteredKindSubdirectories(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	ctx := context.Background()
	for _, tc := range []struct {
		kind   resources.AgentKind
		subdir string
	}{
		{resources.ServiceAgent, "services"},
		{resources.ApplicationAgent, "applications"},
		{resources.ModuleAgent, "modules"},
		{resources.ToolboxAgent, "toolboxes"},
		{resources.ProviderAgent, "providers"},
		{resources.SolutionAgent, "solutions"},
	} {
		agent := &resources.Agent{Kind: tc.kind, Publisher: "codefly.dev", Name: "same", Version: "1.0.0"}
		got, err := agent.Path(ctx)
		require.NoError(t, err)
		require.Contains(t, got, filepath.Join("agents", tc.subdir, "codefly.dev", "same__1.0.0"))
	}
	job := &resources.Agent{Kind: resources.JobAgent, Publisher: "codefly.dev", Name: "job", Version: "1.0.0"}
	_, err := job.Path(ctx)
	require.ErrorContains(t, err, "does not support load")
}

func TestServiceAndProviderSameNameDoNotCollide(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	service := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "shared", Version: "1.0.0"}
	provider := &resources.Agent{Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "shared", Version: "1.0.0"}
	servicePath, err := service.Path(context.Background())
	require.NoError(t, err)
	providerPath, err := provider.Path(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, servicePath, providerPath)

	serviceRegistration, err := resources.AgentKindRegistrationFor(service.Kind)
	require.NoError(t, err)
	providerRegistration, err := resources.AgentKindRegistrationFor(provider.Kind)
	require.NoError(t, err)
	require.NotEqual(t, serviceRegistration.GitHubRepository(service.Name), providerRegistration.GitHubRepository(provider.Name))
}
