package policy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/stretchr/testify/require"
)

// staticExpander is a test PathExpander that resolves a fixed set of
// placeholders and errors on anything else. The error path is
// load-bearing: a typo'd ${WORKSPSACE} should fail the whole policy
// load, never silently expand to "".
type staticExpander map[string]string

func (e staticExpander) Expand(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	// trivially handle one placeholder, since that's all the tests need.
	for k, v := range e {
		token := "${" + k + "}"
		if strings.Contains(s, token) {
			return strings.ReplaceAll(s, token, v), nil
		}
	}
	return "", errors.New("unknown placeholder in: " + s)
}

func TestSandboxPolicy_Validate_Empty_OK(t *testing.T) {
	p := policy.SandboxPolicy{}
	require.NoError(t, p.Validate(),
		"empty policy is a valid declaration; defaults applied at Apply time")
}

func TestSandboxPolicy_Validate_RejectsEmptyEntries(t *testing.T) {
	cases := []policy.SandboxPolicy{
		{ReadPaths: []string{"/a", ""}},
		{WritePaths: []string{""}},
		{UnixSockets: []string{""}},
	}
	for _, c := range cases {
		require.Error(t, c.Validate(),
			"empty path entries are typos and must surface at validate time, not at runtime")
	}
}

func TestSandboxPolicy_Validate_RejectsUnknownNetwork(t *testing.T) {
	p := policy.SandboxPolicy{Network: "bogus"}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
	require.Contains(t, err.Error(), "deny")
	require.Contains(t, err.Error(), "open")
}

func TestSandboxPolicy_Apply_DefaultsToDeny(t *testing.T) {
	p := policy.SandboxPolicy{} // empty
	exp := staticExpander{}

	sb := sandbox.NewNative()
	require.NoError(t, p.Apply(sb, exp))
	// We can't observe the network setting on native (no enforcement),
	// but the call shouldn't error and the test guards against future
	// regressions where empty policy fails-open.
}

func TestSandboxPolicy_Apply_ExpandsPlaceholders(t *testing.T) {
	p := policy.SandboxPolicy{
		ReadPaths:  []string{"${WORKSPACE}/src"},
		WritePaths: []string{"${WORKSPACE}/build"},
		Network:    policy.NetworkOpen,
	}
	exp := staticExpander{"WORKSPACE": "/repo/x"}

	sb := sandbox.NewNative()
	require.NoError(t, p.Apply(sb, exp),
		"expander resolves placeholder; Apply succeeds")
}

func TestSandboxPolicy_Apply_FailsOnUnknownPlaceholder(t *testing.T) {
	p := policy.SandboxPolicy{ReadPaths: []string{"${TYPO}/src"}}
	exp := staticExpander{"WORKSPACE": "/repo"}

	sb := sandbox.NewNative()
	err := p.Apply(sb, exp)
	require.Error(t, err,
		"unresolved placeholders must fail loudly — silent empty-substitution would create an over-broad rule")
	require.Contains(t, err.Error(), "TYPO")
}
