package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

const PipAuditVersion = "2.10.1"

// Python scans a Python project rooted at dir.
// Vulnerabilities are read from a hashed requirements snapshot exported from
// uv.lock with --frozen. Outdated dependencies are resolved from the same
// frozen uv graph; neither operation inspects the ambient interpreter or
// site-packages.
//
// Required tools must be present. Missing vulnerability tooling is an
// incomplete audit and fails instead of returning a false clean result.
func Python(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "PYTHON"}
	tools := []string{"uv-export"}
	if !have("uv") {
		return nil, fmt.Errorf("python locked audit unavailable: uv is not installed")
	}
	requirements, err := runCmd(ctx, dir, "uv", "export", "--frozen", "--format", "requirements.txt",
		"--no-emit-project", "--no-emit-workspace", "--no-emit-local")
	if err != nil {
		return nil, fmt.Errorf("uv locked requirements export failed: %w", err)
	}
	requirementsFile, err := os.CreateTemp("", "codefly-audit-*.requirements.txt")
	if err != nil {
		return nil, fmt.Errorf("create locked audit snapshot: %w", err)
	}
	requirementsPath := requirementsFile.Name()
	defer os.Remove(requirementsPath)
	if _, err := requirementsFile.Write(requirements); err != nil {
		requirementsFile.Close()
		return nil, fmt.Errorf("write locked audit snapshot: %w", err)
	}
	if err := requirementsFile.Close(); err != nil {
		return nil, fmt.Errorf("close locked audit snapshot: %w", err)
	}

	name := "pip-audit"
	args := []string{"--strict", "--progress-spinner", "off", "--require-hashes", "--disable-pip", "-r", requirementsPath, "-f", "json"}
	tool := "pip-audit"
	if !have(name) {
		if !have("uvx") {
			return nil, fmt.Errorf("pip-audit unavailable: neither pip-audit nor uvx is installed")
		}
		name = "uvx"
		args = []string{"--from", "pip-audit==" + PipAuditVersion, "pip-audit", "--strict", "--progress-spinner", "off", "--require-hashes", "--disable-pip", "-r", requirementsPath, "-f", "json"}
		tool = "pip-audit@" + PipAuditVersion
	}
	tools = append(tools, tool)
	// pip-audit exits non-zero on findings, which is normal — output is
	// still returned, so only an empty output means it failed to run.
	out, err := runCmd(ctx, dir, name, args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("pip-audit failed: %w", err)
	}
	findings, err := parsePipAudit(out)
	if err != nil {
		return nil, err
	}
	res.Findings = findings

	if includeOutdated {
		tools = append(tools, "uv-tree-outdated")
		o, err := runCmd(ctx, dir, "uv", "tree", "--frozen", "--outdated", "--color", "never")
		if err != nil {
			return nil, fmt.Errorf("uv tree --outdated failed: %w", err)
		}
		res.Outdated = parseUVTreeOutdated(o)
	}

	res.Tool = strings.Join(tools, "+")
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
		return nil, err
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

var uvOutdatedPattern = regexp.MustCompile(`(?m)^[\s\p{So}\p{Pd}│├└─]*([^\s]+) v([^\s]+) \(latest: v([^\s\)]+)\)`)

// parseUVTreeOutdated consumes uv's stable human-readable annotation:
// "package v1.2.3 (latest: v1.4.0)". uv currently has no JSON tree output.
func parseUVTreeOutdated(out []byte) []*builderv0.OutdatedDep {
	var deps []*builderv0.OutdatedDep
	for _, match := range uvOutdatedPattern.FindAllSubmatch(out, -1) {
		deps = append(deps, &builderv0.OutdatedDep{
			Package:     string(match[1]),
			Current:     string(match[2]),
			LatestMajor: string(match[3]),
		})
	}
	return deps
}
