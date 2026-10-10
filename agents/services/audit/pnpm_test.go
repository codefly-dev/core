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
	for _, bad := range []string{"null", `{"error":{}}`, `{"a":{}}`, `{"b":{"latest":"2.0.0"}}`} {
		_, err := parsePnpmOutdated(locked, []byte(bad))
		require.Error(t, err)
	}
}

// TestPnpmOutdatedSkipsPnpmDiagnosticsBeforeTheReport: pnpm writes "WARN …"
// lines to stdout ahead of its --json report (seen 2026-10-09 in a release
// conformance gate: `parse pnpm outdated: invalid character 'W' looking for
// beginning of value`). The diagnostics are not evidence; the report is.
func TestPnpmOutdatedSkipsPnpmDiagnosticsBeforeTheReport(t *testing.T) {
	locked := []byte(`[{"dependencies":{"left-pad":{"version":"1.0.0"}}}]`)
	report := []byte("WARN  GET https://registry.npmjs.org/left-pad error (ECONNRESET). Will retry in 10 seconds. 2 retries left.\n" +
		" WARN  deprecated subdependencies found: foo@1.0.0\n" +
		"[WARN] Unsupported engine: wanted: {\"node\":\">=26\"} (current: {\"node\":\"v24.21.0\",\"pnpm\":\"11.17.0\"})\n" +
		`{"left-pad":{"current":"1.0.0","wanted":"1.0.0","latest":"1.3.0"}}` + "\n")
	outdated, err := parsePnpmOutdated(locked, report)
	if err != nil {
		t.Fatalf("parse with leading diagnostics: %v", err)
	}
	if len(outdated) != 1 || outdated[0].GetPackage() != "left-pad" {
		t.Fatalf("outdated = %+v, want left-pad", outdated)
	}
	if _, err := parsePnpmOutdated(locked, []byte("WARN  nothing but noise\n[WARN] still noise\n")); err == nil {
		t.Fatal("a report with no JSON must still fail to parse")
	}
	bracketed := []byte("[WARN] Unsupported engine: wanted: {\"node\":\">=26\"}\n" + `[{"dependencies":{"left-pad":{"version":"1.0.0"}}}]` + "\n")
	if _, err := parsePnpmOutdated(bracketed, report); err != nil {
		t.Fatalf("the locked list with a bracketed warning: %v", err)
	}
}
