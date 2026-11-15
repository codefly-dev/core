package solutionhost_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	modulecontract "github.com/codefly-dev/core/contracts/module"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// composed is a fixture's composition: the class it is delivered to and
// its configurations, group by group.
type composed struct {
	Class  string                       `json:"class"`
	Values map[string]map[string]string `json:"values"`
}

func (c composed) infos() []*basev0.ConfigurationInformation {
	var out []*basev0.ConfigurationInformation
	for _, group := range slices.Sorted(maps.Keys(c.Values)) {
		info := &basev0.ConfigurationInformation{Name: group}
		for _, key := range slices.Sorted(maps.Keys(c.Values[group])) {
			info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: key, Value: c.Values[group][key]})
		}
		out = append(out, info)
	}
	return out
}

// TestDeclareRendersWhatTheCLIRendered holds Declare to the CLI's own output
// before the render moved here: each testdata/declare/<case>/
// declaration.golden.json was written by cli 68630378's moduleAuthority lines
// and its ScopeCeilings, Roles, OwnerAware, SharedKinds and
// InstallationDeclaration readers, on the same contract and composition. The
// move changes no byte.
func TestDeclareRendersWhatTheCLIRendered(t *testing.T) {
	cases, err := os.ReadDir(filepath.Join("testdata", "declare"))
	require.NoError(t, err)
	require.NotEmpty(t, cases)
	for _, entry := range cases {
		t.Run(entry.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", "declare", entry.Name())
			raw, err := os.ReadFile(filepath.Join(dir, "composition.json"))
			require.NoError(t, err)
			var c composed
			require.NoError(t, json.Unmarshal(raw, &c))
			declaration, _, err := solutionhost.DeclareModule(dir, "", nil, solutionhost.Resolver(c.infos(), c.Class, nil))
			require.NoError(t, err)
			got, err := json.MarshalIndent(declaration, "", "  ")
			require.NoError(t, err)
			want, err := os.ReadFile(filepath.Join(dir, "declaration.golden.json"))
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(got))
		})
	}
}

func TestDeclareModuleRefusesWhatTheRenderRefuses(t *testing.T) {
	resolve := solutionhost.Resolver(composed{Values: map[string]map[string]string{"ledger": {"store-audience": "store"}}}.infos(), "", nil)
	t.Run("a principal other than the module's name", func(t *testing.T) {
		_, _, err := solutionhost.DeclareModule(filepath.Join("testdata", "declare", "module"), "journal", nil, resolve)
		require.ErrorContains(t, err, `module journal publishes a contract whose principal is "ledger"`)
	})
	t.Run("the retired roles declaration", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, solutionhost.RetiredRolesFileName), []byte("roles: []\n"), 0o600))
		_, _, err := solutionhost.DeclareModule(dir, "", nil, resolve)
		require.ErrorContains(t, err, "roles.codefly.yaml is no longer read")
	})
	t.Run("no contract declares nothing", func(t *testing.T) {
		declaration, resolved, err := solutionhost.DeclareModule(t.TempDir(), "ledger", nil, resolve)
		require.NoError(t, err)
		require.Nil(t, declaration)
		require.Nil(t, resolved)
	})
	t.Run("a slot the composition does not fill", func(t *testing.T) {
		_, _, err := solutionhost.DeclareModule(filepath.Join("testdata", "declare", "module"), "ledger", nil, solutionhost.Resolver(nil, "", nil))
		require.ErrorIs(t, err, modulecontract.ErrUnresolvedSlot)
	})
}

// Every record is handed to core's resolution as supplied: a key supplied
// public and secret resolves as the secret, which core refuses as a slot value.
func TestConfigurationValuesKeepEveryRecord(t *testing.T) {
	infos := []*basev0.ConfigurationInformation{
		{Name: "ledger", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "store-audience", Value: "store"}}},
		{Name: "ledger", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "store-audience", Value: "store", Secret: true}}},
		{Name: "other", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "store-audience", Value: "elsewhere"}}},
	}
	records, err := solutionhost.ConfigurationValues(infos).Records("ledger")
	require.NoError(t, err)
	require.Equal(t, []modulecontract.Record{{Key: "store-audience", Value: "store"}, {Key: "store-audience", Value: "store", Secret: true}}, records)
	_, _, err = solutionhost.DeclareModule(filepath.Join("testdata", "declare", "module"), "ledger", nil, solutionhost.Resolver(infos, "", nil))
	require.ErrorIs(t, err, modulecontract.ErrSecretSlot)
}

// Core's resolution kit drives ConfigurationValues over one group's records,
// held the way a workspace configuration read holds them: a provider that
// selected among them would fail it.
func TestTheResolutionKitRunsThroughConfigurationValues(t *testing.T) {
	modulecontract.RunResolution(t, func(group string, records []modulecontract.Record) modulecontract.Values {
		values := make([]*basev0.ConfigurationValue, 0, len(records))
		for _, record := range records {
			values = append(values, &basev0.ConfigurationValue{Key: record.Key, Value: record.Value, Secret: record.Secret})
		}
		return solutionhost.ConfigurationValues([]*basev0.ConfigurationInformation{{Name: group, ConfigurationValues: values}})
	})
}
