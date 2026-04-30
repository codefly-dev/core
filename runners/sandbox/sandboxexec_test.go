//go:build darwin

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSandboxExec_BuildProfile_HomeDenyRules verifies the secret-path
// deny rules: when HOME is set, the profile contains explicit denies
// for ~/.ssh, ~/.aws, etc. These are the "best-effort hardening"
// layer — without them an unsandboxed read of $HOME/.ssh/id_rsa
// would succeed. The test stubs the package-level `getenv` so we
// don't depend on the runner's actual HOME and can assert the rules
// regardless of whether ~/.ssh exists.
func TestSandboxExec_BuildProfile_HomeDenyRules(t *testing.T) {
	defer func(prev func(string) string) { getenv = prev }(getenv)
	getenv = func(key string) string {
		if key == "HOME" {
			return "/Users/synthetic"
		}
		return ""
	}

	sb, err := newSandboxExec()
	require.NoError(t, err)
	profile, err := sb.buildProfile()
	require.NoError(t, err)

	for _, secret := range []string{".ssh", ".aws", ".config/codefly/secrets", ".gnupg"} {
		require.True(t,
			strings.Contains(profile, "/Users/synthetic/"+secret),
			"profile must explicitly deny ~/%s; not found in:\n%s", secret, profile)
	}
}

// TestSandboxExec_BuildProfile_NoHome confirms the home-secret rules
// are skipped (best-effort) when HOME isn't set in the environment.
// The profile is still valid; the secret-deny layer is just absent.
func TestSandboxExec_BuildProfile_NoHome(t *testing.T) {
	defer func(prev func(string) string) { getenv = prev }(getenv)
	getenv = func(string) string { return "" }

	sb, err := newSandboxExec()
	require.NoError(t, err)
	profile, err := sb.buildProfile()
	require.NoError(t, err)

	// No "$HOME/.ssh" denial — the rule was skipped because HOME=="".
	require.NotContains(t, profile, ".ssh",
		"with HOME unset, no per-home secret deny should be emitted")
}
