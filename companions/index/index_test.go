package companionindex_test

import (
	"testing"

	companionindex "github.com/codefly-dev/core/companions/index"
	"github.com/stretchr/testify/require"
)

func TestTheRegisteredIndexWinsOverTheEnvironment(t *testing.T) {
	t.Setenv(companionindex.Environment, "proto=1.0.0")
	got, err := companionindex.Version("proto")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", got, "a spawned process runs the index its caller passed")

	require.NoError(t, companionindex.Register(companionindex.Index{"proto": "2.0.0"}))
	t.Cleanup(func() { require.NoError(t, companionindex.Register(nil)) })
	got, err = companionindex.Version("proto")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", got)
}

func TestTheEnvironmentFormRoundTrips(t *testing.T) {
	index := companionindex.Index{"proto": "0.0.30", "go": "0.0.20"}
	require.Equal(t, "go=0.0.20,proto=0.0.30", index.Environ())
	parsed, err := companionindex.ParseEnviron(index.Environ())
	require.NoError(t, err)
	require.Equal(t, index, parsed)
}

func TestAnIndexRefusesWhatIsNotAVersion(t *testing.T) {
	for _, raw := range []string{"proto", "proto=latest", "proto=1.0.0,proto=1.0.1", "=1.0.0"} {
		_, err := companionindex.ParseEnviron(raw)
		require.Errorf(t, err, "%q", raw)
	}
	_, err := companionindex.Parse([]byte("proto: not-a-version\n"))
	require.Error(t, err)
	parsed, err := companionindex.Parse([]byte("proto: v0.0.30\n"))
	require.NoError(t, err)
	require.Equal(t, "0.0.30", parsed["proto"], "written the way the image is tagged")
}

func TestAnUnnamedCompanionIsAnError(t *testing.T) {
	require.NoError(t, companionindex.Register(companionindex.Index{"proto": "1.0.0"}))
	t.Cleanup(func() { require.NoError(t, companionindex.Register(nil)) })
	_, err := companionindex.Version("go")
	require.ErrorContains(t, err, `names no "go" companion`)
}

func TestFromRecipesReadsThisCheckout(t *testing.T) {
	index, err := companionindex.FromRecipes("../..")
	require.NoError(t, err)
	for _, name := range []string{"codefly", "execution", "go", "node", "proto", "python"} {
		require.Containsf(t, index, name, "recipe %s", name)
	}
}
