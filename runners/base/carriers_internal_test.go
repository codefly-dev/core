package base

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestProcessCarriersCount134EffectiveBindings(t *testing.T) {
	var bindings []*resources.EnvironmentVariable
	for i := 0; i < 134; i++ {
		bindings = append(bindings, resources.Env(fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__RUNNABLE_BINDINGS__OP_%03d", i), strings.Repeat("x", 2800)))
	}
	require.NoError(t, resources.CheckProcessEnvironment(bindings))
	require.ErrorIs(t, resources.CheckProcessEnvironment(append(append([]*resources.EnvironmentVariable{}, bindings...), bindings...)), resources.ErrEnvironmentLimit)
	carriers, err := prepareProcessCarriers(bindings, bindings)
	require.NoError(t, err)
	t.Cleanup(carriers.release)
	require.Empty(t, carriers.dir, "no spill or file transport change")
	require.Equal(t, resources.EnvironmentVariableAsStrings(bindings), carriers.environ)
	var distinct []*resources.EnvironmentVariable
	for _, env := range bindings {
		distinct = append(distinct, resources.Env(env.Key+"_OTHER", env.Value))
	}
	_, err = prepareProcessCarriers(bindings, distinct)
	require.ErrorIs(t, err, resources.ErrEnvironmentLimit, "genuinely oversized unique environment still refuses")
}

func TestProcessCarrierOverridesPrecedeMaterialization(t *testing.T) {
	key := "CODEFLY__WORKSPACE_SECRET_CONFIGURATION__VAULT__VALUE"
	file := &resources.EnvironmentVariable{Key: key, Value: strings.Repeat("s", resources.FileCarrierThreshold+1), File: true, Secret: true}
	inline := resources.Env(key, "changed")
	for _, test := range []struct {
		name         string
		base, winner *resources.EnvironmentVariable
	}{
		{"file to inline", file, inline},
		{"inline to file", inline, file},
		{"changed inline", resources.Env(key, "old"), inline},
	} {
		t.Run(test.name, func(t *testing.T) {
			carriers, err := prepareProcessCarriers([]*resources.EnvironmentVariable{test.base}, []*resources.EnvironmentVariable{test.winner})
			require.NoError(t, err)
			t.Cleanup(carriers.release)
			require.Len(t, carriers.environ, 1)
			if !test.winner.File {
				require.Empty(t, carriers.dir)
				require.Equal(t, []string{key + "=changed"}, carriers.environ)
				return
			}
			resolved, err := resources.ResolveFileCarriers(carriers.environ)
			require.NoError(t, err)
			require.Equal(t, test.winner.ValueAsString(), resolved[key])
			entries, err := os.ReadDir(carriers.dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			info, err := entries[0].Info()
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})
	}
}

func TestProcessCarrierDistinctAliasStillRefusesAtRead(t *testing.T) {
	key := "CODEFLY__WORKSPACE_CONFIGURATION__GROUP__VALUE"
	file := filepath.Join(t.TempDir(), "value")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	carriers, err := prepareProcessCarriers([]*resources.EnvironmentVariable{resources.Env(key, "inline")}, []*resources.EnvironmentVariable{resources.Env(resources.FileCarrierKey(key), file)})
	require.NoError(t, err)
	t.Cleanup(carriers.release)
	require.Len(t, carriers.environ, 2, "exact keys remain distinct")
	_, err = resources.ResolveFileCarriers(carriers.environ)
	require.ErrorIs(t, err, resources.ErrFileCarrier)
}
