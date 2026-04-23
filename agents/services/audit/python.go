package audit

import (
	"context"
	"encoding/json"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Python scans a Python project rooted at dir.
// Vulnerabilities: pip-audit -f json. Outdated: pip list --outdated --format json.
//
// Both tools must be on PATH. If either is missing, that scanner is
// silently skipped — Tool reflects what actually ran.
func Python(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "PYTHON", Tool: "missing"}
	tools := []string{}

	if have("pip-audit") {
		tools = append(tools, "pip-audit")
		out, _ := runCmd(ctx, dir, "pip-audit", "-f", "json")
		findings, err := parsePipAudit(out)
		if err != nil {
			return nil, err
		}
		res.Findings = findings
	}

	if includeOutdated && have("pip") {
		tools = append(tools, "pip-outdated")
		o, _ := runCmd(ctx, dir, "pip", "list", "--outdated", "--format", "json")
		res.Outdated = parsePipOutdated(o)
	}

	if len(tools) > 0 {
		res.Tool = strings.Join(tools, "+")
	}
	return res, nil
}

// pip-audit -f json shape:
// { "dependencies": [{ "name": "...", "version": "...", "vulns": [{id, fix_versions, description}] }] }
type pipAuditOutput struct {
	Dependencies []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Vulns   []struct {
			ID          string   `json:"id"`
			FixVersions []string `json:"fix_versions"`
			Description string   `json:"description"`
		} `json:"vulns"`
	} `json:"dependencies"`
}

func parsePipAudit(out []byte) ([]*builderv0.AuditFinding, error) {
	if len(out) == 0 {
		return nil, nil
	}
	var p pipAuditOutput
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, nil
	}
	var findings []*builderv0.AuditFinding
	for _, d := range p.Dependencies {
		for _, v := range d.Vulns {
			fixed := ""
			if len(v.FixVersions) > 0 {
				fixed = v.FixVersions[0]
			}
			findings = append(findings, &builderv0.AuditFinding{
				// pip-audit doesn't classify severity; treat all as HIGH so they
				// surface in fail_on_vuln. Severity refinement is a follow-up
				// (would require a second OSV API call per finding).
				Severity:       builderv0.AuditFinding_HIGH,
				Id:             v.ID,
				Package:        d.Name,
				CurrentVersion: d.Version,
				FixedVersion:   fixed,
				Summary:        v.Description,
				Url:            "https://osv.dev/vulnerability/" + v.ID,
			})
		}
	}
	return findings, nil
}

// pip list --outdated --format json shape: [{name, version, latest_version, latest_filetype}]
type pipOutdatedEntry struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	LatestVersion string `json:"latest_version"`
}

func parsePipOutdated(out []byte) []*builderv0.OutdatedDep {
	if len(out) == 0 {
		return nil
	}
	var entries []pipOutdatedEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil
	}
	var deps []*builderv0.OutdatedDep
	for _, e := range entries {
		// pip list doesn't separate safe vs major; conservative treatment
		// is to put the available upgrade in latest_major and let the
		// upgrade helper reapply semver constraints.
		deps = append(deps, &builderv0.OutdatedDep{
			Package:     e.Name,
			Current:     e.Version,
			LatestSafe:  e.LatestVersion,
			LatestMajor: e.LatestVersion,
		})
	}
	return deps
}
