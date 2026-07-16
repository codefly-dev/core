package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

const (
	TrivyVersion = "v0.72.0"
	TrivyImage   = "aquasec/trivy@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f"
)

// Docker scans a container image (e.g. postgres:16, redis:7) for known
// CVEs using trivy. Used by Docker-only agents (postgres, redis,
// neo4j, temporal, vault) where the codefly service is the official
// upstream image — there is no application code to scan, just the
// distribution itself.
//
// trivy must be available; missing tooling is an incomplete audit and fails.
func Docker(ctx context.Context, image string) (*Result, error) {
	res := &Result{Language: "DOCKER"}
	if image == "" {
		return nil, fmt.Errorf("trivy audit requires a container image reference")
	}
	name := "trivy"
	args := []string{"image", "--quiet", "--format", "json", "--severity", "HIGH,CRITICAL", image}
	res.Tool = "trivy"
	if !have(name) {
		if !have("docker") {
			return nil, fmt.Errorf("trivy audit unavailable: neither trivy nor docker is installed")
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("resolve Trivy cache: %w", err)
		}
		cache = filepath.Join(cache, "codefly", "trivy")
		if err := os.MkdirAll(cache, 0o700); err != nil {
			return nil, fmt.Errorf("create Trivy cache: %w", err)
		}
		name = "docker"
		args = []string{"run", "--rm", "--network", "bridge", "-v", cache + ":/root/.cache/trivy", TrivyImage,
			"image", "--quiet", "--format", "json", "--severity", "HIGH,CRITICAL", image}
		res.Tool = "trivy@" + TrivyVersion
	}

	// --quiet suppresses progress; --format json gives parseable output;
	// --severity HIGH,CRITICAL keeps the output focused on actionable
	// findings (postgres images can carry hundreds of LOW/MEDIUM CVEs
	// in OS packages that the user can't fix without rebuilding upstream).
	// trivy signals "found vulnerabilities" via a non-zero exit code, which
	// is NOT a run failure — keep parsing stdout in that case. Only treat it
	// as a genuine failure (binary error, image pull failure, etc.) when the
	// command both errored AND produced no output to parse.
	out, err := runCmd(ctx, "", name, args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("trivy audit failed: %w", err)
	}
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
