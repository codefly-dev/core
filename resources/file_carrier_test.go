package resources_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
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
	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
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
		// A value goes by file only above FileCarrierThreshold, so an empty
		// file is a broken delivery, not an unset value.
		"an empty file": {resources.FileCarrierKey(public) + "=" + empty},
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

// An environment above the limit delivers its largest values by file until it
// fits; one that fits is delivered exactly as it always was, and the input is
// never modified.
func TestSpillToFileCarriersDeliversTheLargestValuesByFileOnlyWhenNeeded(t *testing.T) {
	var envs []*resources.EnvironmentVariable
	for i := 0; i < 40; i++ {
		envs = append(envs, resources.Env(fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__G__K%02d", i), strings.Repeat("v", (10+i)<<10)))
	}
	envs = append(envs, resources.Env("SMALL", "x"), resources.Env("not a file name", strings.Repeat("v", 30<<10)))
	require.ErrorIs(t, resources.CheckProcessEnvironment(envs), resources.ErrEnvironmentLimit)

	spilled := spillAt("/carriers", envs, 0)
	for _, env := range envs {
		require.False(t, env.File, "the input is not modified")
	}
	var files int
	for _, env := range spilled {
		if env.File {
			files++
			require.True(t, strings.HasPrefix(env.Key, "CODEFLY__WORKSPACE_CONFIGURATION__G__K"), env.Key)
		}
	}
	require.Positive(t, files)
	require.False(t, byKey(spilled)["SMALL"].File)
	require.False(t, byKey(spilled)["not a file name"].File, "a key that cannot name a file is never chosen")

	planned, delivered, err := resources.PlanFileCarriers("/carriers", spilled)
	require.NoError(t, err)
	require.Len(t, delivered, files)
	require.NoError(t, resources.CheckProcessEnvironment(planned))

	fits := envs[:3]
	// Values already delivered by file still carry a path inline, and a spilled
	// value reads back whole through the resolution an SDK uses.
	already := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__BIG", strings.Repeat("b", 40<<10))
	already.File = true
	mixed := append([]*resources.EnvironmentVariable{already, nil}, envs...)
	planned, delivered, err = resources.PlanFileCarriers("/carriers", spillAt("/carriers", mixed, 0))
	require.NoError(t, err)
	require.NoError(t, resources.CheckProcessEnvironment(planned))
	dir := t.TempDir()
	materialized, err := resources.MaterializeFileCarriers(dir, dir, spillAt(dir, envs, 0))
	require.NoError(t, err)
	read, err := resources.ResolveFileCarriers(resources.EnvironmentVariableAsStrings(materialized))
	require.NoError(t, err)
	require.Len(t, read, files)
	for key, value := range read {
		require.Equal(t, byKey(envs)[key].ValueAsString(), value)
	}

	require.Equal(t, fits, spillAt("/carriers", fits, 0), "an environment that fits is untouched")
	require.Equal(t, fits, spillAt("/carriers", fits, 1<<10))
	require.NotEqual(t, fits, spillAt("/carriers", fits, resources.MaxCarriedEnvironmentBytes), "reserved bytes count against the budget")
}

func spillAt(dir string, envs []*resources.EnvironmentVariable, reserved int) []*resources.EnvironmentVariable {
	return resources.SpillToFileCarriers(envs, reserved, resources.PlannedPathLen(dir))
}

func spilledKeys(envs []*resources.EnvironmentVariable) []string {
	var keys []string
	for _, env := range envs {
		if env.File {
			keys = append(keys, env.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Values of the same size spill in the same order whatever order the
// environment arrives in, so a render is the same every time.
func TestSpillToFileCarriersChoosesTiesByKeyNotByArrivalOrder(t *testing.T) {
	var envs []*resources.EnvironmentVariable
	for i := 0; i < 30; i++ {
		envs = append(envs, resources.Env(fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__G__K%02d", i), strings.Repeat("v", 20<<10)))
	}
	want := spilledKeys(spillAt("/carriers", envs, 0))
	require.NotEmpty(t, want)
	require.Less(t, len(want), len(envs), "only some of the equal values spill")
	for seed := 0; seed < 20; seed++ {
		shuffled := append([]*resources.EnvironmentVariable(nil), envs...)
		rand.New(rand.NewSource(int64(seed))).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		require.Equal(t, want, spilledKeys(spillAt("/carriers", shuffled, 0)))
	}
}

// What is counted is the path the file really has: an environment that fits
// exactly with its real paths is not spilled, and one byte more is.
func TestSpillToFileCarriersCountsTheRealPath(t *testing.T) {
	const dir = "/c"
	delivered := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__BIG", strings.Repeat("b", 40<<10))
	delivered.File = true
	carrier := resources.FileCarrierSize(delivered.Key, resources.PlannedPathLen(dir)(delivered.Key))
	_, files, err := resources.PlanFileCarriers(dir, []*resources.EnvironmentVariable{delivered})
	require.NoError(t, err)
	planned, _, _ := resources.PlanFileCarriers(dir, []*resources.EnvironmentVariable{delivered})
	require.Len(t, files, 1)
	require.Equal(t, resources.EnvironmentBytes(planned), carrier, "the counted carrier is the one planned")

	room := resources.MaxCarriedEnvironmentBytes - carrier
	key := "CODEFLY__WORKSPACE_CONFIGURATION__G__FILL"
	fill := func(extra int) []*resources.EnvironmentVariable {
		return []*resources.EnvironmentVariable{delivered, resources.Env(key, strings.Repeat("f", room-len(key)-2+extra))}
	}
	exact := fill(0)
	require.Equal(t, exact, spillAt(dir, exact, 0), "an environment that fits is untouched")
	require.NoError(t, resources.CheckProcessEnvironment(planned))
	over := spillAt(dir, fill(1), 0)
	require.True(t, byKey(over)[key].File, "one byte over spills")
}

// A secret is never written to a file by the spill, and a raw variable is
// never chosen because the carrier would give it back under another name:
// what cannot move refuses the environment, and what can moves instead.
func TestSpillToFileCarriersNeverMovesASecretOrARawVariable(t *testing.T) {
	big := strings.Repeat("v", 120<<10)
	secret := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__PLAIN_KEY_SECRET_FLAG", big)
	secret.Secret = true
	namespaced := resources.Env(resources.WorkspaceSecretConfigurationPrefix+"__G__TOKEN", big)
	raw := resources.Env("RAW_TOKEN", big)
	public := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__PUBLIC", strings.Repeat("p", 160<<10))
	envs := []*resources.EnvironmentVariable{secret, namespaced, raw, public}
	require.Greater(t, resources.EnvironmentBytes(envs), resources.MaxCarriedEnvironmentBytes)

	spilled := spillAt("/carriers", envs, 0)
	require.Equal(t, []string{public.Key}, spilledKeys(spilled), "only the public CODEFLY__ value moves")
	for _, env := range []*resources.EnvironmentVariable{secret, namespaced, raw} {
		require.False(t, byKey(spilled)[env.Key].File, env.Key)
	}

	// Nothing but secrets and raw variables: the environment is refused, never spilled.
	only := spillAt("/carriers", []*resources.EnvironmentVariable{secret, namespaced, raw, resources.Env("RAW_TWO", big), resources.Env("RAW_THREE", big)}, 0)
	require.Empty(t, spilledKeys(only))
	require.ErrorIs(t, resources.CheckProcessEnvironment(only), resources.ErrEnvironmentLimit)

	// What does move comes back under its own key.
	_, files, err := resources.PlanFileCarriers("/carriers", spilled)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, public.Key, resources.CarriedKey(resources.FileCarrierKey(public.Key)))
}

// Only a configuration value moves: an endpoint, a credential-like key in a
// public namespace, and a value whose carrier would be no smaller stay inline,
// and a carrier set empty is set aside rather than read.
func TestSpillToFileCarriersMovesOnlyWhatReadersResolveAndWhatShrinks(t *testing.T) {
	big := strings.Repeat("v", 120<<10)
	envs := []*resources.EnvironmentVariable{
		resources.Env("CODEFLY__SERVICE_ENDPOINT__M__S__REST", big),
		resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__AUTH__API_TOKEN", big),
		resources.Env("CODEFLY__SERVICE_CONFIGURATION__M__S__DB__PASSWORD", big),
		resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__CATALOG", big),
		resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__"+strings.Repeat("K", 200), "x"),
	}
	spilled := spillAt("/carriers", envs, 100<<10)
	require.Equal(t, []string{"CODEFLY__WORKSPACE_CONFIGURATION__G__CATALOG"}, spilledKeys(spilled))

	tiny := []*resources.EnvironmentVariable{resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__G__"+strings.Repeat("K", 200), "x")}
	require.Equal(t, tiny, spillAt("/carriers", tiny, resources.MaxCarriedEnvironmentBytes), "a carrier larger than its value is not worth it")

	resolved, err := resources.ResolveFileCarriers([]string{resources.FileCarrierKey("CODEFLY__X") + "=", "CODEFLY__X=inline"})
	require.NoError(t, err)
	require.Empty(t, resolved)
}
