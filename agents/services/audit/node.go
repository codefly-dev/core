package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Node scans a Node package rooted at dir (must contain package.json
// + lockfile). Vulnerabilities: `npm audit --json`. Outdated:
// `npm outdated --json` (returns {} when nothing outdated).
//
// Both commands exit non-zero on findings, which is normal — we still
// parse the JSON output. Missing npm is an incomplete audit and fails.
func Node(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "TYPESCRIPT", Tool: "npm-audit"}
	if !have("npm") {
		return nil, fmt.Errorf("npm audit unavailable: npm is not installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("npm audit requires package-lock.json")
		}
		return nil, fmt.Errorf("inspect package-lock.json: %w", err)
	}

	out, err := runCmd(ctx, dir, "npm", "audit", "--json")
	if err != nil && len(out) == 0 {
		// `npm audit` failed completely (e.g. missing tool/lockfile);
		// nothing to parse — non-zero exit on findings still yields output.
		return nil, fmt.Errorf("npm audit failed: %w", err)
	}
	findings, err := parseNpmAudit(out)
	if err != nil {
		return nil, err
	}
	res.Findings = findings

	if includeOutdated {
		// package-lock-only makes "current" come from the release lock rather
		// than an arbitrary node_modules tree on the operator's machine.
		o, err := runCmd(ctx, dir, "npm", "outdated", "--json", "--package-lock-only")
		if err != nil && len(o) == 0 {
			// `npm outdated` failed completely; skip outdated portion.
			return nil, fmt.Errorf("npm outdated failed: %w", err)
		}
		res.Outdated = parseNpmOutdated(o)
		res.Tool = "npm-audit+outdated"
	}
	return res, nil
}

// npmAudit is the schema of `npm audit --json` (npm 7+).
// We only consume the vulnerabilities map.
type npmAudit struct {
	Vulnerabilities map[string]struct {
		Name         string `json:"name"`
		Severity     string `json:"severity"`
		Via          []any  `json:"via"`
		FixAvailable any    `json:"fixAvailable"` // bool or {name,version,isSemVerMajor}
		Range        string `json:"range"`
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
				return nil, err
			}
		} else {
			return nil, err
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
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].GetPackage() != findings[j].GetPackage() {
			return findings[i].GetPackage() < findings[j].GetPackage()
		}
		return findings[i].GetId() < findings[j].GetId()
	})
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
	sort.Slice(deps, func(i, j int) bool { return deps[i].GetPackage() < deps[j].GetPackage() })
	return deps
}
