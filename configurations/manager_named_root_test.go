package configurations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A consumer's render is judged on the workspace configuration it RECEIVES.
// The check already was (cli#883), but the composition-root read took no
// names: it resolved every root group and returned on the first refusal, so a
// withheld credential whose reference is a fault — ambiguous, say — for a
// service that never receives it refused that service's render. The consumer
// names what it receives, and only that is resolved. Restrict scopes by service
// origin, not by group, and the per-dependency read is the strict path — a
// different semantic — so neither was the way out.
//
// The fixture: the composition root provides two groups run-wide. The
// `work-context` group references an endpoint the consumer resolves; the
// `vault` credential references one that is AMBIGUOUS for it (a token naming
// no endpoint, satisfied by two it may reach), which the run-wide path refuses
// rather than omits. Beside them, a composed module's own group and a name two
// composed modules defined incompatibly — the two things a name can be without
// being the root's.
func namedRootGroupsManager(t *testing.T) *configurations.Manager {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host-a
    path: ../host-a
  - name: host-b
    path: ../host-b
`)
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env",
		"authority-url=${endpoint:platform/authority/primary}/v1\n")
	writeConfigurationFile(t, root, "solution/configurations/local/vault.secret.env",
		"token-endpoint=${endpoint:platform/authority/grpc}\n")
	for _, host := range []string{"host-a", "host-b"} {
		writeConfigurationFile(t, root, host+"/module.codefly.yaml", "kind: module\nname: "+host+"\nservices: []\n")
		writeConfigurationFile(t, root, host+"/configurations/local/observability.env", "OBSERVABILITY_URL="+host+"-observability\n")
	}
	writeConfigurationFile(t, root, "host-a/configurations/local/telemetry.env", "TELEMETRY_URL=host-a-telemetry\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	authority := []*resources.Endpoint{
		{Module: "platform", Service: "authority", Name: "hidden", API: "grpc", Visibility: "private"},
		{Module: "platform", Service: "authority", Name: "primary", API: "grpc", Visibility: "public"},
		{Module: "platform", Service: "authority", Name: "secondary", API: "grpc", Visibility: "public"},
	}
	declared := resources.DeclaredEndpoints(func(unique string) ([]*resources.Endpoint, bool) {
		return authority, unique == "platform/authority"
	})
	mapping := func(name, address string) *basev0.NetworkMapping {
		return &basev0.NetworkMapping{
			Endpoint:  &basev0.Endpoint{Module: "platform", Service: "authority", Name: name, Api: "grpc"},
			Instances: []*basev0.NetworkInstance{{Address: address, Access: resources.NewNativeNetworkAccess()}},
		}
	}
	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager = manager.ForConsumerModule("payments", declared)
	manager.WithLoader(loader).
		WithNetworkMappings([]*basev0.NetworkMapping{
			mapping("hidden", "http://localhost:1"),
			mapping("primary", "http://localhost:2"),
			mapping("secondary", "http://localhost:3"),
		}, resources.NewNativeNetworkAccess()).
		WithRunProducers(func(unique string) bool { return unique == "platform/authority" })
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))
	return manager
}

// Named, the read resolves the named groups and nothing else: the credential
// this consumer does not receive is never interpolated, so its ambiguous
// reference cannot refuse the read. What is named is resolved, not returned
// raw — the reference the consumer can satisfy is its address.
func TestTheCompositionRootReadResolvesOnlyTheNamedGroups(t *testing.T) {
	ctx := context.Background()
	manager := namedRootGroupsManager(t)

	confs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx, "work-context")
	require.NoError(t, err, "a fault in a group this consumer does not receive must not refuse the groups it does")
	require.Len(t, confs, 1)
	url, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "authority-url")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:2/v1", url, "the named group is resolved for the consumer, not handed back raw")
	_, err = resources.FindWorkspaceConfiguration(ctx, confs, "vault")
	require.Error(t, err, "a group that was not named is not delivered")

	// A name listed twice is one group, read once.
	confs, err = manager.GetCompositionRootWorkspaceConfigurations(ctx, "work-context", "work-context")
	require.NoError(t, err)
	require.Len(t, confs, 1)
}

// With no names the read is what it was: every root group, and the first fault
// refuses it, naming the group and key. Narrowing the named read did not
// loosen the unnamed one.
func TestTheCompositionRootReadWithoutNamesStillRefusesTheFirstFault(t *testing.T) {
	ctx := context.Background()
	manager := namedRootGroupsManager(t)

	confs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)
	require.Contains(t, err.Error(), "vault/token-endpoint")
	require.Nil(t, confs)

	// Naming the faulty group reaches the same refusal: naming narrows which
	// groups are judged, never how.
	_, err = manager.GetCompositionRootWorkspaceConfigurations(ctx, "vault", "work-context")
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)
	require.Contains(t, err.Error(), "vault/token-endpoint")
}

// A name the composition root does not provide run-wide refuses the read by
// name — never an empty answer, never the known groups without it. Each kind
// of name says what it is instead: a composed module's group, a name composed
// modules defined incompatibly, a name nothing loaded.
func TestTheCompositionRootReadRefusesANameTheRootDoesNotProvide(t *testing.T) {
	ctx := context.Background()
	manager := namedRootGroupsManager(t)

	confs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx, "telemetry")
	require.ErrorIs(t, err, configurations.ErrNotACompositionRootConfiguration)
	require.Contains(t, err.Error(), `"telemetry"`)
	require.Contains(t, err.Error(), "composed module's group")
	require.Nil(t, confs)

	_, err = manager.GetCompositionRootWorkspaceConfigurations(ctx, "observability")
	require.ErrorIs(t, err, configurations.ErrNotACompositionRootConfiguration)
	require.ErrorIs(t, err, configurations.ErrConfigurationConflict, "an ambiguous name carries the loader's diagnostic")
	require.Contains(t, err.Error(), `"observability"`)

	_, err = manager.GetCompositionRootWorkspaceConfigurations(ctx, "nope")
	require.ErrorIs(t, err, configurations.ErrNotACompositionRootConfiguration)
	require.Contains(t, err.Error(), `"nope"`)
	require.Contains(t, err.Error(), "vault, work-context", "the refusal names what the root does provide")

	// One unknown name among known ones refuses the whole read: a named read
	// never answers with less than it was asked for.
	confs, err = manager.GetCompositionRootWorkspaceConfigurations(ctx, "work-context", "nope")
	require.ErrorIs(t, err, configurations.ErrNotACompositionRootConfiguration)
	require.Nil(t, confs)
}
