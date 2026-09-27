package runnablefixture_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/internal/runnablefixture"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
)

// lookupIn reads the runnable-bindings group of configuration the way the
// SDK's WorkspaceValue does.
func lookupIn(configuration *basev0.Configuration) func(string) (string, error) {
	return func(key string) (string, error) {
		for _, value := range configuration.GetInfos()[0].GetConfigurationValues() {
			if value.GetKey() == key {
				return value.GetValue(), nil
			}
		}
		return "", fmt.Errorf("no value for %s", key)
	}
}

// Derivation is a function of its inputs: the same workspace derives the same
// packages, the same bindings, the same descriptor sets and so the same
// digests, byte for byte, every time.
func TestDerivationAndDigestsAreStable(t *testing.T) {
	first, err := runnablefixture.Build(24, 2)
	require.NoError(t, err)
	second, err := runnablefixture.Build(24, 2)
	require.NoError(t, err)
	for i := range first.Endpoints {
		require.Equal(t, first.Endpoints[i].Set, second.Endpoints[i].Set)
		require.Equal(t, first.Endpoints[i].Reference, second.Endpoints[i].Reference)
	}
	for i := range first.Operations {
		require.Equal(t, first.Operations[i].Value, second.Operations[i].Value)
	}
	digests := map[string]bool{}
	for _, operation := range first.Operations {
		installed, err := runnablefixture.Install(operation.Value, lookupIn(first.Configuration()))
		require.NoError(t, err)
		require.False(t, digests[installed.Binding.GetDigest()], "every operation is its own binding")
		digests[installed.Binding.GetDigest()] = true
	}
}

// Every operation of a twenty-four operation workspace installs, and the
// operations of one owner endpoint resolve the same set: one copy per
// endpoint, not per operation.
func TestEveryOperationInstallsFromOneSetPerEndpoint(t *testing.T) {
	workspace, err := runnablefixture.Build(24, 2)
	require.NoError(t, err)
	configuration := workspace.Configuration()
	require.Len(t, configuration.GetInfos()[0].GetConfigurationValues(), 24+2)
	perSet := map[string]int{}
	for _, operation := range workspace.Operations {
		installed, err := runnablefixture.Install(operation.Value, lookupIn(configuration))
		require.NoError(t, err, operation.Key)
		require.Equal(t, operation.Method, installed.Binding.GetServiceOperation().GetOperation())
		perSet[installed.SetDigest]++
	}
	require.Equal(t, map[string]int{
		workspace.Endpoints[0].Reference.Digest: 12,
		workspace.Endpoints[1].Reference.Digest: 12,
	}, perSet)
}

// Sharing the descriptors changed no package and no binding: the shared and
// the embedded form carry the same bytes for both, so no identity moves when
// a workspace regenerates.
func TestSharingChangesNoPackageOrBindingIdentity(t *testing.T) {
	workspace, err := runnablefixture.Build(4, 2)
	require.NoError(t, err)
	embedded := workspace.Embedded().GetInfos()[0].GetConfigurationValues()
	for i, operation := range workspace.Operations {
		var shared, old map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(operation.Value), &shared))
		require.NoError(t, json.Unmarshal([]byte(embedded[i].GetValue()), &old))
		require.JSONEq(t, string(old["package"]), string(shared["package"]))
		require.JSONEq(t, string(old["binding"]), string(shared["binding"]))
	}
}

// A worker never installs from descriptors whose identity it did not check:
// a set from another generation, a truncated one, a missing one and the
// embedded form each fail the installation closed.
func TestInstallationFailsClosed(t *testing.T) {
	workspace, err := runnablefixture.Build(2, 2)
	require.NoError(t, err)
	operation := workspace.Operations[0]
	key, err := operation.Endpoint.Reference.Key()
	require.NoError(t, err)

	other := runnable.EncodeDescriptorSet(workspace.Endpoints[1].Set)
	_, err = runnablefixture.Install(operation.Value, func(string) (string, error) { return other, nil })
	require.ErrorIs(t, err, runnable.ErrDescriptorSetMismatch, "a set from another endpoint or generation")

	truncated := runnable.EncodeDescriptorSet(operation.Endpoint.Set[:len(operation.Endpoint.Set)/2])
	_, err = runnablefixture.Install(operation.Value, func(string) (string, error) { return truncated, nil })
	require.ErrorIs(t, err, runnable.ErrDescriptorSetMismatch, "a truncated set")

	_, err = runnablefixture.Install(operation.Value, func(k string) (string, error) {
		return "", fmt.Errorf("no value for %s", k)
	})
	require.ErrorContains(t, err, key, "a missing set is named by its key")

	embedded := workspace.Embedded().GetInfos()[0].GetConfigurationValues()[0].GetValue()
	_, err = runnablefixture.Install(embedded, lookupIn(workspace.Configuration()))
	require.ErrorIs(t, err, runnable.ErrEmbeddedDescriptors)

	tampered := strings.Replace(operation.Value, `"revision":"install-00"`, `"revision":"install-99"`, 1)
	require.NotEqual(t, operation.Value, tampered)
	_, err = runnablefixture.Install(tampered, lookupIn(workspace.Configuration()))
	require.ErrorIs(t, err, runnable.ErrInvalid, "a binding whose bytes are not its digest")
}

// The regression test for the failure this design removes: every value the
// runnable-bindings group of a twenty-four operation workspace puts in a
// process environment stays under Linux's 128 KiB per-string limit, and the
// whole of it under 1 MiB. It fails if any single value would not.
func TestNoEnvironmentValueReachesTheLinuxStringLimit(t *testing.T) {
	workspace, err := runnablefixture.Build(24, 2)
	require.NoError(t, err)
	envs, err := resources.ConfigurationAsEnvironmentVariables(workspace.Configuration(), "local", false)
	require.NoError(t, err)
	delivered, err := resources.MaterializeFileCarriers(t.TempDir(), "/var/run/codefly/configuration", envs)
	require.NoError(t, err)
	total := 0
	for _, entry := range resources.EnvironmentVariableAsStrings(delivered) {
		require.Less(t, len(entry)+1, resources.MaxEnvironmentStringBytes, strings.SplitN(entry, "=", 2)[0])
		total += len(entry) + 1
	}
	require.Less(t, total, 1<<20)
	for _, endpoint := range workspace.Endpoints {
		require.Greater(t, len(runnable.EncodeDescriptorSet(endpoint.Set)), resources.FileCarrierThreshold,
			"the fixture's descriptor sets must be large enough to need a file, or this test proves nothing")
	}
}
