package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestEffectiveEnvironmentPreservesLastCompleteVariable(t *testing.T) {
	old := &resources.EnvironmentVariable{Key: "CODEFLY__VALUE", Value: "old", File: true, Secret: true}
	other := resources.Env("OTHER", "unchanged")
	winner := resources.Env(old.Key, "new")
	alias := resources.Env(resources.FileCarrierKey(old.Key), "/separate")
	base := []*resources.EnvironmentVariable{old, nil, other}
	process := []*resources.EnvironmentVariable{winner, alias}
	got := resources.EffectiveEnvironmentVariables(base, process)
	require.Equal(t, []*resources.EnvironmentVariable{other, winner, alias}, got)
	require.Same(t, winner, got[1])
	require.False(t, got[1].File)
	require.False(t, got[1].Secret)
	require.Equal(t, []*resources.EnvironmentVariable{old, nil, other}, base)
	require.Equal(t, []*resources.EnvironmentVariable{winner, alias}, process)
	got = resources.EffectiveEnvironmentVariables(process, base)
	require.Same(t, old, got[1])
	require.True(t, got[1].File)
	require.True(t, got[1].Secret)
}
