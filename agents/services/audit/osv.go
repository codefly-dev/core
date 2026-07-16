package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

const (
	OSVScannerVersion = "v2.4.0"
	OSVScannerImage   = "ghcr.io/google/osv-scanner@sha256:5116601dedc01c1c580eb92371883ec052fc4c13c3fbc109d621a63ac416d475"
)

// OSVLockfile scans one authoritative ecosystem lockfile. A host-installed
// osv-scanner is honored; otherwise Codefly runs the official multi-platform
// image pinned by manifest-list digest with the project mounted read-only.
// The scanner submits package names and versions to OSV.dev, never source.
func OSVLockfile(ctx context.Context, dir, lockfile, language string) (*Result, error) {
	if filepath.Base(lockfile) != lockfile {
		return nil, fmt.Errorf("OSV lockfile must be a base name: %q", lockfile)
	}
	lockPath := filepath.Join(dir, lockfile)
	info, err := os.Stat(lockPath)
	if err != nil {
		return nil, fmt.Errorf("OSV audit requires %s: %w", lockfile, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("OSV audit lockfile is not regular: %s", lockPath)
	}

	name := "osv-scanner"
	args := []string{"scan", "source", "--format", "json", "--verbosity", "error", "-L", lockfile}
	tool := "osv-scanner"
	runDir := dir
	if !have(name) {
		if !have("docker") {
			return nil, fmt.Errorf("OSV audit unavailable: neither osv-scanner nor docker is installed")
		}
		absoluteLock, err := filepath.Abs(lockPath)
		if err != nil {
			return nil, fmt.Errorf("resolve OSV lockfile path: %w", err)
		}
		name = "docker"
		args = []string{
			"run", "--rm", "--network", "bridge", "--read-only", "--cap-drop", "ALL",
			"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
			"-v", absoluteLock + ":/src/" + lockfile + ":ro",
			OSVScannerImage, "scan", "source", "--format", "json", "--verbosity", "error",
			"-L", "/src/" + lockfile,
		}
		tool = "osv-scanner@" + OSVScannerVersion
		runDir = ""
	}

	out, runErr := runCmd(ctx, runDir, name, args...)
	// Exit 1 means findings. Scanner/runtime errors produce no JSON or an
	// unparseable diagnostic and must not become a false CLEAN result.
	if runErr != nil && len(out) == 0 {
		return nil, fmt.Errorf("OSV audit failed: %w", runErr)
	}
	findings, err := parseOSV(out)
	if err != nil {
		return nil, fmt.Errorf("parse OSV audit: %w", err)
	}
	return &Result{Findings: findings, Tool: tool, Language: language}, nil
}

type osvOutput struct {
	Results []struct {
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Vulnerabilities []osvVulnerability `json:"vulnerabilities"`
			Groups          []struct {
				IDs         []string `json:"ids"`
				MaxSeverity string   `json:"max_severity"`
			} `json:"groups"`
		} `json:"packages"`
	} `json:"results"`
}

type osvVulnerability struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific map[string]any `json:"database_specific"`
	Affected         []struct {
		Package struct {
			Name string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Fixed string `json:"fixed"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

func parseOSV(out []byte) ([]*builderv0.AuditFinding, error) {
	if len(out) == 0 {
		return nil, nil
	}
	var report osvOutput
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, err
	}
	var findings []*builderv0.AuditFinding
	seen := map[string]struct{}{}
	for _, result := range report.Results {
		for _, vulnerablePackage := range result.Packages {
			byID := map[string]*osvVulnerability{}
			for i := range vulnerablePackage.Vulnerabilities {
				vulnerability := &vulnerablePackage.Vulnerabilities[i]
				byID[vulnerability.ID] = vulnerability
			}
			type vulnerabilityGroup struct {
				ids         []string
				maxSeverity string
			}
			groups := make([]vulnerabilityGroup, 0, len(vulnerablePackage.Groups))
			for _, group := range vulnerablePackage.Groups {
				groups = append(groups, vulnerabilityGroup{ids: group.IDs, maxSeverity: group.MaxSeverity})
			}
			if len(groups) == 0 {
				for _, vulnerability := range vulnerablePackage.Vulnerabilities {
					groups = append(groups, vulnerabilityGroup{ids: []string{vulnerability.ID}})
				}
			}
			for _, group := range groups {
				ids := group.ids
				id := preferredOSVID(ids)
				vulnerability := byID[id]
				if vulnerability == nil {
					for _, candidate := range ids {
						if vulnerability = byID[candidate]; vulnerability != nil {
							break
						}
					}
				}
				if vulnerability == nil {
					continue
				}
				key := vulnerablePackage.Package.Name + "@" + vulnerablePackage.Package.Version + ":" + strings.Join(sortedCopy(ids), ",")
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				summary := strings.TrimSpace(vulnerability.Summary)
				if summary == "" {
					summary = firstParagraph(vulnerability.Details)
				}
				findings = append(findings, &builderv0.AuditFinding{
					Severity:       osvSeverity(vulnerability, group.maxSeverity),
					Id:             id,
					Package:        vulnerablePackage.Package.Name,
					CurrentVersion: vulnerablePackage.Package.Version,
					FixedVersion:   osvFixedVersion(vulnerability, vulnerablePackage.Package.Name),
					Summary:        summary,
					Url:            "https://osv.dev/vulnerability/" + id,
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].GetPackage() != findings[j].GetPackage() {
			return findings[i].GetPackage() < findings[j].GetPackage()
		}
		return findings[i].GetId() < findings[j].GetId()
	})
	return findings, nil
}

func preferredOSVID(ids []string) string {
	for _, prefix := range []string{"RUSTSEC-", "GO-", "PYSEC-", "GHSA-", "CVE-"} {
		for _, id := range ids {
			if strings.HasPrefix(id, prefix) {
				return id
			}
		}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return "OSV-UNKNOWN"
}

func osvSeverity(vulnerability *osvVulnerability, groupScore string) builderv0.AuditFinding_Severity {
	if raw, ok := vulnerability.DatabaseSpecific["severity"].(string); ok {
		return namedSeverity(raw)
	}
	if severity, ok := numericSeverity(groupScore); ok {
		return severity
	}
	for _, severity := range vulnerability.Severity {
		if result, ok := numericSeverity(severity.Score); ok {
			return result
		}
	}
	// A vulnerability with no normalized severity must remain release-gating;
	// UNKNOWN would otherwise slip through HIGH-or-above policies.
	return builderv0.AuditFinding_HIGH
}

func numericSeverity(value string) (builderv0.AuditFinding_Severity, bool) {
	score, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return builderv0.AuditFinding_UNKNOWN, false
	}
	switch {
	case score >= 9:
		return builderv0.AuditFinding_CRITICAL, true
	case score >= 7:
		return builderv0.AuditFinding_HIGH, true
	case score >= 4:
		return builderv0.AuditFinding_MEDIUM, true
	default:
		return builderv0.AuditFinding_LOW, true
	}
}

func namedSeverity(value string) builderv0.AuditFinding_Severity {
	switch strings.ToUpper(value) {
	case "LOW":
		return builderv0.AuditFinding_LOW
	case "MODERATE", "MEDIUM":
		return builderv0.AuditFinding_MEDIUM
	case "HIGH":
		return builderv0.AuditFinding_HIGH
	case "CRITICAL":
		return builderv0.AuditFinding_CRITICAL
	default:
		return builderv0.AuditFinding_HIGH
	}
}

func osvFixedVersion(vulnerability *osvVulnerability, packageName string) string {
	var versions []string
	for _, affected := range vulnerability.Affected {
		if affected.Package.Name != "" && affected.Package.Name != packageName {
			continue
		}
		for _, affectedRange := range affected.Ranges {
			for _, event := range affectedRange.Events {
				if event.Fixed != "" {
					versions = append(versions, event.Fixed)
				}
			}
		}
	}
	sort.Strings(versions)
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

func firstParagraph(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.Index(value, "\n\n"); i >= 0 {
		value = value[:i]
	}
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

// Rust audits the exact Cargo.lock graph through the managed OSV adapter.
func Rust(ctx context.Context, dir string) (*Result, error) {
	return OSVLockfile(ctx, dir, "Cargo.lock", "RUST")
}
