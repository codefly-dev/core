package resources_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func workspaceGroup(values map[string]string, secret bool) *basev0.Configuration {
	info := &basev0.ConfigurationInformation{Name: "group"}
	for key, value := range values {
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: key, Value: value, Secret: secret})
	}
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}
}

func byKey(envs []*resources.EnvironmentVariable) map[string]*resources.EnvironmentVariable {
	out := map[string]*resources.EnvironmentVariable{}
	for _, env := range envs {
		out[env.Key] = env
	}
	return out
}

func TestFileCarrierKeyRoundTrips(t *testing.T) {
	key := "CODEFLY__WORKSPACE_CONFIGURATION__GROUP__VALUE"
	require.Equal(t, "CODEFLY__FILE__WORKSPACE_CONFIGURATION__GROUP__VALUE", resources.FileCarrierKey(key))
	require.True(t, resources.IsFileCarrierKey(resources.FileCarrierKey(key)))
	require.False(t, resources.IsFileCarrierKey(key))
	require.Equal(t, key, resources.CarriedKey(resources.FileCarrierKey(key)))
	require.True(t, resources.IsSecretCarrierKey("CODEFLY__WORKSPACE_SECRET_CONFIGURATION__GROUP__VALUE"))
	require.False(t, resources.IsSecretCarrierKey(key))
}

// The carrier is decided by the value's size alone, by every emitter.
func TestAValueAboveTheThresholdIsDeliveredByFile(t *testing.T) {
	large := strings.Repeat("v", resources.FileCarrierThreshold+1)
	small := strings.Repeat("v", resources.FileCarrierThreshold)
	for _, secret := range []bool{false, true} {
		envs, err := resources.ConfigurationAsEnvironmentVariables(workspaceGroup(map[string]string{"large": large, "small": small}, secret), "local", secret)
		require.NoError(t, err)
		require.Len(t, envs, 2)
		for _, env := range envs {
			require.Equal(t, strings.HasSuffix(env.Key, "__LARGE"), env.File, env.Key)
			require.Equal(t, secret, env.Secret, env.Key)
		}
	}
}

func TestMaterializedCarriersRoundTripAndArePrivate(t *testing.T) {
	large := strings.Repeat("0123456789", resources.FileCarrierThreshold)
	envs, err := resources.ConfigurationAsEnvironmentVariables(workspaceGroup(map[string]string{"large": large, "small": "s"}, true), "local", true)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "carriers")
	delivered, err := resources.MaterializeFileCarriers(dir, dir, envs)
	require.NoError(t, err)
	require.NoError(t, resources.CheckProcessEnvironment(delivered))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	carried := byKey(delivered)
	key := "CODEFLY__WORKSPACE_SECRET_CONFIGURATION__GROUP__LARGE"
	require.NotContains(t, carried, key, "a file-delivered value is absent from the environment")
	path := carried[resources.FileCarrierKey(key)].ValueAsString()
	require.True(t, filepath.IsAbs(path))
	file, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), file.Mode().Perm())

	resolved, err := resources.ResolveFileCarriers(resources.EnvironmentVariableAsStrings(delivered))
	require.NoError(t, err)
	require.Equal(t, map[string]string{key: large}, resolved)

	// Materializing the same value again names the same file.
	again, err := resources.MaterializeFileCarriers(dir, dir, envs)
	require.NoError(t, err)
	require.Equal(t, path, byKey(again)[resources.FileCarrierKey(key)].ValueAsString())
}

func TestResolveFileCarriersFailsClosed(t *testing.T) {
	dir := t.TempDir()
	value := filepath.Join(dir, "value")
	require.NoError(t, os.WriteFile(value, []byte("content"), 0o600))
	public := "CODEFLY__WORKSPACE_CONFIGURATION__GROUP__VALUE"
	secret := "CODEFLY__WORKSPACE_SECRET_CONFIGURATION__GROUP__VALUE"

	resolved, err := resources.ResolveFileCarriers([]string{resources.FileCarrierKey(public) + "=" + value, "OTHER=x"})
	require.NoError(t, err)
	require.Equal(t, "content", resolved[public])

	cases := map[string][]string{
		"inline and by file": {public + "=inline", resources.FileCarrierKey(public) + "=" + value},
		"relative path":      {resources.FileCarrierKey(public) + "=value"},
		"missing file":       {resources.FileCarrierKey(public) + "=" + filepath.Join(dir, "absent")},
		"a directory":        {resources.FileCarrierKey(public) + "=" + dir},
	}
	for name, environ := range cases {
		_, err := resources.ResolveFileCarriers(environ)
		require.ErrorIs(t, err, resources.ErrFileCarrier, name)
	}

	// A secret file others can read is refused; a public one is not.
	shared := filepath.Join(dir, "shared")
	require.NoError(t, os.WriteFile(shared, []byte("secret"), 0o600))
	require.NoError(t, os.Chmod(shared, 0o644))
	_, err = resources.ResolveFileCarriers([]string{resources.FileCarrierKey(secret) + "=" + shared})
	require.ErrorIs(t, err, resources.ErrFileCarrier)
	require.NotContains(t, err.Error(), "secret\n")
	_, err = resources.ResolveFileCarriers([]string{resources.FileCarrierKey(public) + "=" + shared})
	require.NoError(t, err)
	// Group access is what a pod's fsGroup needs; it is admitted.
	require.NoError(t, os.Chmod(shared, 0o640))
	_, err = resources.ResolveFileCarriers([]string{resources.FileCarrierKey(secret) + "=" + shared})
	require.NoError(t, err)

	// Kubernetes presents each key as a link into its data directory.
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(value, link))
	resolved, err = resources.ResolveFileCarriers([]string{resources.FileCarrierKey(public) + "=" + link})
	require.NoError(t, err)
	require.Equal(t, "content", resolved[public])
}

func TestCheckProcessEnvironmentRefusesWhatExecWould(t *testing.T) {
	under := strings.Repeat("v", resources.MaxEnvironmentStringBytes-len("KEY=")-2)
	require.NoError(t, resources.CheckProcessEnvironment([]*resources.EnvironmentVariable{resources.Env("KEY", under)}))

	secretValue := strings.Repeat("s", resources.MaxEnvironmentStringBytes)
	err := resources.CheckProcessEnvironment([]*resources.EnvironmentVariable{resources.Env("KEY", secretValue)})
	require.ErrorIs(t, err, resources.ErrEnvironmentLimit)
	require.Contains(t, err.Error(), "KEY")
	require.NotContains(t, err.Error(), "sss", "the error names the key, never the value")

	var many []*resources.EnvironmentVariable
	for i := 0; i < 20; i++ {
		many = append(many, resources.Env("KEY_"+strings.Repeat("A", i), strings.Repeat("v", 30<<10)))
	}
	err = resources.CheckProcessEnvironment(many)
	require.ErrorIs(t, err, resources.ErrEnvironmentLimit)
	require.Contains(t, err.Error(), "largest")

	unmaterialized := resources.Env("KEY", "v")
	unmaterialized.File = true
	require.ErrorIs(t, resources.CheckProcessEnvironment([]*resources.EnvironmentVariable{unmaterialized}), resources.ErrEnvironmentLimit)
}
