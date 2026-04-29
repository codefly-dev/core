package policy_test

import (
	"testing"

	"github.com/codefly-dev/core/policy"
	"github.com/stretchr/testify/require"
)

func TestCanonicalRegistry_BuiltinFallbacks(t *testing.T) {
	r := policy.NewCanonicalRegistry()

	// All known dangerous binaries route to a deny-with-hint.
	for _, bin := range []string{"git", "docker", "nix", "kubectl", "helm", "curl", "wget"} {
		d := r.Lookup(bin)
		require.NotNil(t, d, "expected fallback routing for %q", bin)
		require.True(t, d.Routed)
		require.Empty(t, d.Owner, "fallback should have no owner; got %q", d.Owner)
		require.NotEmpty(t, d.Reason, "fallback must carry a reason for diagnostics")
	}

	// A binary nobody routes is, well, not routed.
	require.Nil(t, r.Lookup("ls"), "ls is not in the canonical-deny set")
}

func TestCanonicalRegistry_PluginClaim_ReplacesFallback(t *testing.T) {
	r := policy.NewCanonicalRegistry()

	require.NoError(t, r.Claim("git-toolbox", "git"))

	d := r.Lookup("git")
	require.NotNil(t, d)
	require.True(t, d.Routed)
	require.Equal(t, "git-toolbox", d.Owner,
		"explicit plugin claim must replace the fallback")
}

func TestCanonicalRegistry_PathStripped(t *testing.T) {
	r := policy.NewCanonicalRegistry()

	// `/usr/bin/git` and `git` should both route — the bash parser
	// gives us paths sometimes (resolved by `which`), but the
	// canonical decision is on the leaf name.
	require.NotNil(t, r.Lookup("/usr/bin/git"))
	require.NotNil(t, r.Lookup("git"))
}

func TestCanonicalRegistry_DoubleClaim_Errors(t *testing.T) {
	r := policy.NewCanonicalRegistry()

	require.NoError(t, r.Claim("git-toolbox", "git"))
	err := r.Claim("evil-toolbox", "git")
	require.Error(t, err, "two plugins claiming the same binary must be a load-time error")
	require.Contains(t, err.Error(), "git", "error must name the contested binary")
	require.Contains(t, err.Error(), "git-toolbox", "error must name the existing owner")
}

func TestCanonicalRegistry_SamePluginReclaim_Idempotent(t *testing.T) {
	r := policy.NewCanonicalRegistry()

	require.NoError(t, r.Claim("git-toolbox", "git"))
	require.NoError(t, r.Claim("git-toolbox", "git"),
		"a plugin reclaiming its own binary on hot-reload must not error")
}

func TestCanonicalRegistry_EmptyOwner_Rejected(t *testing.T) {
	r := policy.NewCanonicalRegistry()
	require.Error(t, r.Claim("", "git"),
		"empty owner is a programmer error; surface it loudly")
}

func TestCanonicalRegistry_EmptyBinary_Rejected(t *testing.T) {
	r := policy.NewCanonicalRegistry()
	require.Error(t, r.Claim("git-toolbox", ""))
	require.Error(t, r.Claim("git-toolbox", "git", ""),
		"any empty entry in the list invalidates the whole claim")
}

func TestCanonicalRegistry_Owners_SortedSnapshot(t *testing.T) {
	r := policy.NewCanonicalRegistry()
	require.NoError(t, r.Claim("git-toolbox", "git"))

	owners := r.Owners()
	// Sorted by binary; smoke-test that "docker" comes before "git".
	var seen []string
	for _, o := range owners {
		seen = append(seen, o.Binary)
	}
	require.Contains(t, seen, "git")
	require.Contains(t, seen, "docker")

	// Verify ordering — docker before git (alphabetical).
	dockerIdx, gitIdx := -1, -1
	for i, b := range seen {
		switch b {
		case "docker":
			dockerIdx = i
		case "git":
			gitIdx = i
		}
	}
	require.Less(t, dockerIdx, gitIdx, "Owners() snapshot must be sorted by binary")
}
