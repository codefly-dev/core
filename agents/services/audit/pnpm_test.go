package audit

import (
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestPnpmAuditPreservesRealAdvisories(t *testing.T) {
	data, err := os.ReadFile("testdata/pnpm-advisories.json")
	require.NoError(t, err)
	findings, err := parsePnpmAudit(data)
	require.NoError(t, err)
	require.Len(t, findings, 2)
	require.Equal(t, "GHSA-28wg-ghj8-5hjv", findings[0].Id)
	require.Equal(t, "nanoid", findings[0].Package)
	require.Equal(t, "5.1.11", findings[0].CurrentVersion)
	require.Equal(t, ">=5.1.16", findings[0].FixedVersion)
	require.Equal(t, builderv0.AuditFinding_HIGH, findings[0].Severity)
	require.Equal(t, builderv0.AuditFinding_MEDIUM, findings[1].Severity)
}

func TestPnpmAuditCannotTurnErrorsIntoCleanEvidence(t *testing.T) {
	for _, input := range []string{"", "{}", `{"error":{"code":"ENETWORK"}}`, `{"advisories":{},"metadata":{}}`, `{"advisories":{},"metadata":{"vulnerabilities":{"high":1}}}`, `{"advisories":{"1":{"module_name":"x"}},"metadata":{"vulnerabilities":{"high":1}}}`} {
		_, err := parsePnpmAudit([]byte(input))
		require.Error(t, err, input)
	}
	findings, err := parsePnpmAudit([]byte(`{"advisories":{},"metadata":{"vulnerabilities":{"high":0}}}`))
	require.NoError(t, err)
	require.Empty(t, findings)
	_, err = PnpmWithOptions(t.Context(), t.TempDir(), NodeOptions{})
	require.ErrorContains(t, err, "pnpm-lock.yaml")
}

func TestPnpmOutdatedUsesLockedVersion(t *testing.T) {
	locked := []byte(`[{"dependencies":{"a":{"version":"1.0.0"}}}]`)
	output := []byte(`{"a":{"current":"9.9.9","wanted":"1.1.0","latest":"2.0.0"}}`)
	result, err := parsePnpmOutdated(locked, output)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "1.0.0", result[0].Current)
	require.Equal(t, "1.1.0", result[0].LatestSafe)
	require.Equal(t, "2.0.0", result[0].LatestMajor)
	for _, bad := range []string{"null", `{"error":{}}`, `{"b":{"latest":"2.0.0"}}`} {
		_, err := parsePnpmOutdated(locked, []byte(bad))
		require.Error(t, err)
	}
}
