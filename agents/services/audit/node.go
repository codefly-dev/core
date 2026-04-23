package audit

import (
	"context"
	"encoding/json"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Node scans a Node package rooted at dir (must contain package.json
// + lockfile). Vulnerabilities: `npm audit --json`. Outdated:
// `npm outdated --json` (returns {} when nothing outdated).
//
// Both commands exit non-zero on findings, which is normal — we still
// parse the JSON output. Missing tool → Tool="missing".
func Node(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "TYPESCRIPT", Tool: "npm-audit"}
	if !have("npm") {
		res.Tool = "missing"
		return res, nil
	}

	out, _ := runCmd(ctx, dir, "npm", "audit", "--json")
	findings, err := parseNpmAudit(out)
	if err != nil {
		return nil, err
	}
	res.Findings = findings

	if includeOutdated {
		o, _ := runCmd(ctx, dir, "npm", "outdated", "--json")
		res.Outdated = parseNpmOutdated(o)
		res.Tool = "npm-audit+outdated"
	}
	return res, nil
}

// npmAudit is the schema of `npm audit --json` (npm 7+).
// We only consume the vulnerabilities map.
type npmAudit struct {
	Vulnerabilities map[string]struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
		Via      []any  `json:"via"`
		FixAvailable any `json:"fixAvailable"` // bool or {name,version,isSemVerMajor}
		Range string `json:"range"`
	} `json:"vulnerabilities"`
}

func parseNpmAudit(out []byte) ([]*builderv0.AuditFinding, error) {
	if len(out) == 0 {
		return nil, nil
	}
	var a npmAudit
	if err := json.Unmarshal(out, &a); err != nil {
		// Malformed (e.g. npm printed a warning before the JSON);
		// try to recover by slicing from the first '{'.
		if i := strings.IndexByte(string(out), '{'); i >= 0 {
			if err := json.Unmarshal(out[i:], &a); err != nil {
				return nil, nil // give up gracefully
			}
		} else {
			return nil, nil
		}
	}
	var findings []*builderv0.AuditFinding
	for _, v := range a.Vulnerabilities {
		// Each "via" entry is either a string (parent dep) or an object with
		// {source, name, dependency, title, url, severity}. We pull the title
		// + url from the first object form for context.
		var summary, url string
		for _, raw := range v.Via {
			if obj, ok := raw.(map[string]any); ok {
				if t, _ := obj["title"].(string); t != "" {
					summary = t
				}
				if u, _ := obj["url"].(string); u != "" {
					url = u
				}
				break
			}
		}
		findings = append(findings, &builderv0.AuditFinding{
			Severity:       npmSeverity(v.Severity),
			Id:             v.Name,
			Package:        v.Name,
			CurrentVersion: v.Range,
			FixedVersion:   npmFixedVersion(v.FixAvailable),
			Summary:        summary,
			Url:            url,
		})
	}
	return findings, nil
}

func npmSeverity(s string) builderv0.AuditFinding_Severity {
	switch strings.ToLower(s) {
	case "low":
		return builderv0.AuditFinding_LOW
	case "moderate":
		return builderv0.AuditFinding_MEDIUM
	case "high":
		return builderv0.AuditFinding_HIGH
	case "critical":
		return builderv0.AuditFinding_CRITICAL
	default:
		return builderv0.AuditFinding_UNKNOWN
	}
}

func npmFixedVersion(fix any) string {
	if obj, ok := fix.(map[string]any); ok {
		if v, _ := obj["version"].(string); v != "" {
			return v
		}
	}
	return ""
}

// npm outdated --json shape: { "<pkg>": {current, wanted, latest} }.
// `wanted` is the highest match against the package.json range
// (== latest_safe). `latest` includes major bumps.
type npmOutdatedEntry struct {
	Current string `json:"current"`
	Wanted  string `json:"wanted"`
	Latest  string `json:"latest"`
}

func parseNpmOutdated(out []byte) []*builderv0.OutdatedDep {
	if len(out) == 0 {
		return nil
	}
	m := map[string]npmOutdatedEntry{}
	if err := json.Unmarshal(out, &m); err != nil {
		return nil
	}
	var deps []*builderv0.OutdatedDep
	for name, e := range m {
		deps = append(deps, &builderv0.OutdatedDep{
			Package:     name,
			Current:     e.Current,
			LatestSafe:  e.Wanted,
			LatestMajor: e.Latest,
		})
	}
	return deps
}
