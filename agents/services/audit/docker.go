package audit

import (
	"context"
	"encoding/json"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Docker scans a container image (e.g. postgres:16, redis:7) for known
// CVEs using trivy. Used by Docker-only agents (postgres, redis,
// neo4j, temporal, vault) where the codefly service is the official
// upstream image — there is no application code to scan, just the
// distribution itself.
//
// trivy must be on PATH; if missing, returns Tool="missing" with no
// findings (the audit still succeeds).
func Docker(ctx context.Context, image string) (*Result, error) {
	res := &Result{Language: "DOCKER", Tool: "missing"}
	if image == "" {
		return res, nil
	}
	if !have("trivy") {
		return res, nil
	}
	res.Tool = "trivy"

	// --quiet suppresses progress; --format json gives parseable output;
	// --severity HIGH,CRITICAL keeps the output focused on actionable
	// findings (postgres images can carry hundreds of LOW/MEDIUM CVEs
	// in OS packages that the user can't fix without rebuilding upstream).
	out, _ := runCmd(ctx, "", "trivy", "image",
		"--quiet",
		"--format", "json",
		"--severity", "HIGH,CRITICAL",
		image,
	)
	res.Findings = parseTrivy(out)
	return res, nil
}

// trivyOutput captures the subset of `trivy image --format json` we need.
type trivyOutput struct {
	Results []struct {
		Target          string `json:"Target"`
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
			PrimaryURL       string `json:"PrimaryURL"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

func parseTrivy(out []byte) []*builderv0.AuditFinding {
	if len(out) == 0 {
		return nil
	}
	var t trivyOutput
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	var findings []*builderv0.AuditFinding
	for _, r := range t.Results {
		for _, v := range r.Vulnerabilities {
			findings = append(findings, &builderv0.AuditFinding{
				Severity:       trivySeverity(v.Severity),
				Id:             v.VulnerabilityID,
				Package:        v.PkgName,
				CurrentVersion: v.InstalledVersion,
				FixedVersion:   v.FixedVersion,
				Summary:        v.Title,
				Url:            v.PrimaryURL,
			})
		}
	}
	return findings
}

func trivySeverity(s string) builderv0.AuditFinding_Severity {
	switch strings.ToUpper(s) {
	case "LOW":
		return builderv0.AuditFinding_LOW
	case "MEDIUM":
		return builderv0.AuditFinding_MEDIUM
	case "HIGH":
		return builderv0.AuditFinding_HIGH
	case "CRITICAL":
		return builderv0.AuditFinding_CRITICAL
	default:
		return builderv0.AuditFinding_UNKNOWN
	}
}
