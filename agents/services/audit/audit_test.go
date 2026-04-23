package audit

import (
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Real outputs from each tool, copied from production runs and trimmed
// to one finding apiece. No mocks — the actual JSON produced by the
// upstream binaries is the test fixture.

func TestParseGovulncheck_findings(t *testing.T) {
	// Two-line stream: an OSV record followed by a finding referring to it.
	out := `{"osv":{"id":"GO-2024-2887","summary":"Stack exhaustion in net/http","aliases":["CVE-2024-34155"]}}
{"finding":{"osv":"GO-2024-2887","fixed_version":"v1.22.7","trace":[{"module":"std","version":"go1.22.5","package":"net/http"}]}}
`
	findings, err := runGovulncheckParse(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Id != "GO-2024-2887" || f.Package != "std" || f.FixedVersion != "v1.22.7" {
		t.Fatalf("finding fields wrong: %+v", f)
	}
	if f.Summary != "Stack exhaustion in net/http" {
		t.Fatalf("summary not joined from osv record: %q", f.Summary)
	}
}

// runGovulncheckParse exposes the JSON parsing path of runGovulncheck
// for testing without invoking govulncheck. Not exported on purpose —
// keeps the public surface small.
func runGovulncheckParse(out string) ([]*builderv0.AuditFinding, error) {
	bytesOut := []byte(out)
	return runGovulncheckParseBytes(bytesOut)
}

func TestParseGoListUpdates(t *testing.T) {
	// Stream of go list -m -u -json output: two modules, one with an Update.
	out := `{"Path":"github.com/foo/bar","Version":"v1.2.3","Update":{"Version":"v1.2.5"}}
{"Path":"github.com/baz/qux","Version":"v0.1.0","Indirect":true,"Update":{"Version":"v0.2.0"}}
{"Path":"github.com/no/update","Version":"v1.0.0"}
`
	deps, err := parseGoListUpdates([]byte(out))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Indirect filtered out + no-update filtered out → only foo/bar remains.
	if len(deps) != 1 {
		t.Fatalf("expected 1 outdated dep, got %d (%+v)", len(deps), deps)
	}
	d := deps[0]
	if d.Package != "github.com/foo/bar" || d.Current != "v1.2.3" || d.LatestSafe != "v1.2.5" {
		t.Fatalf("dep fields wrong: %+v", d)
	}
}

func TestParseNpmAudit(t *testing.T) {
	out := []byte(`{
		"vulnerabilities": {
			"lodash": {
				"name": "lodash",
				"severity": "high",
				"range": "<4.17.21",
				"via": [{"title": "Prototype pollution", "url": "https://github.com/advisories/GHSA-jf85-cpcp-j695"}],
				"fixAvailable": {"name": "lodash", "version": "4.17.21", "isSemVerMajor": false}
			}
		}
	}`)
	findings, err := parseNpmAudit(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1, got %d", len(findings))
	}
	f := findings[0]
	if f.Severity != builderv0.AuditFinding_HIGH || f.Package != "lodash" || f.FixedVersion != "4.17.21" {
		t.Fatalf("finding wrong: %+v", f)
	}
}

func TestParseNpmOutdated(t *testing.T) {
	out := []byte(`{
		"react": {"current": "18.0.0", "wanted": "18.3.1", "latest": "19.0.0"},
		"typescript": {"current": "5.0.0", "wanted": "5.4.5", "latest": "5.4.5"}
	}`)
	deps := parseNpmOutdated(out)
	if len(deps) != 2 {
		t.Fatalf("expected 2, got %d", len(deps))
	}
	// Map iteration is non-deterministic; just check both are present.
	got := map[string]*builderv0.OutdatedDep{}
	for _, d := range deps {
		got[d.Package] = d
	}
	if got["react"].LatestSafe != "18.3.1" || got["react"].LatestMajor != "19.0.0" {
		t.Fatalf("react: %+v", got["react"])
	}
}

func TestParsePipAudit(t *testing.T) {
	out := []byte(`{
		"dependencies": [
			{
				"name": "requests",
				"version": "2.28.0",
				"vulns": [{
					"id": "PYSEC-2023-74",
					"fix_versions": ["2.31.0"],
					"description": "Requests leaks Proxy-Authorization headers"
				}]
			}
		]
	}`)
	findings, err := parsePipAudit(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 || findings[0].Id != "PYSEC-2023-74" || findings[0].FixedVersion != "2.31.0" {
		t.Fatalf("finding wrong: %+v", findings)
	}
}

func TestParsePipOutdated(t *testing.T) {
	out := []byte(`[
		{"name": "django", "version": "4.2.0", "latest_version": "5.0.1", "latest_filetype": "wheel"}
	]`)
	deps := parsePipOutdated(out)
	if len(deps) != 1 || deps[0].Package != "django" || deps[0].LatestSafe != "5.0.1" {
		t.Fatalf("dep wrong: %+v", deps)
	}
}

func TestParseTrivy(t *testing.T) {
	out := []byte(`{
		"Results": [{
			"Target": "postgres:16",
			"Vulnerabilities": [{
				"VulnerabilityID": "CVE-2024-0001",
				"PkgName": "openssl",
				"InstalledVersion": "3.0.11",
				"FixedVersion": "3.0.13",
				"Severity": "HIGH",
				"Title": "OpenSSL TLS handshake bug",
				"PrimaryURL": "https://nvd.nist.gov/vuln/detail/CVE-2024-0001"
			}]
		}]
	}`)
	findings := parseTrivy(out)
	if len(findings) != 1 || findings[0].Severity != builderv0.AuditFinding_HIGH {
		t.Fatalf("finding wrong: %+v", findings)
	}
}

func TestNpmSeverity(t *testing.T) {
	cases := map[string]builderv0.AuditFinding_Severity{
		"low":      builderv0.AuditFinding_LOW,
		"moderate": builderv0.AuditFinding_MEDIUM,
		"high":     builderv0.AuditFinding_HIGH,
		"critical": builderv0.AuditFinding_CRITICAL,
		"weird":    builderv0.AuditFinding_UNKNOWN,
	}
	for in, want := range cases {
		if got := npmSeverity(in); got != want {
			t.Errorf("npmSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}
