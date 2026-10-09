package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// PnpmWithOptions audits the committed pnpm dependency graph without installing
// packages. Registry or tool errors are incomplete evidence, never a clean scan.
func PnpmWithOptions(ctx context.Context, dir string, options NodeOptions) (*Result, error) {
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err != nil {
		return nil, fmt.Errorf("pnpm audit requires pnpm-lock.yaml: %w", err)
	}
	args := []string{"audit", "--json"}
	tool := "pnpm-audit"
	if !options.IncludeDevDependencies {
		args = append(args, "--prod")
		tool += "-runtime"
	}
	output, runErr := runCmd(ctx, dir, "pnpm", args...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	findings, parseErr := parsePnpmAudit(output)
	if parseErr != nil {
		return nil, errors.Join(fmt.Errorf("pnpm audit: %w", parseErr), runErr)
	}
	if runErr != nil {
		var exit *exec.ExitError
		// pnpm exits 1 for findings. Any other exit, or a nonzero exit with no
		// finding, cannot be accepted merely because stdout contained JSON.
		if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || len(findings) == 0 {
			return nil, fmt.Errorf("pnpm audit failed: %w", runErr)
		}
	}
	result := &Result{Language: "TYPESCRIPT", Tool: tool, Findings: findings}
	if options.IncludeOutdated {
		outdated, err := pnpmOutdated(ctx, dir, options.IncludeDevDependencies)
		if err != nil {
			return nil, err
		}
		result.Outdated = outdated
		result.Tool += "+outdated"
	}
	return result, nil
}

type pnpmAuditReport struct {
	Advisories map[string]struct {
		ID       int    `json:"id"`
		GithubID string `json:"github_advisory_id"`
		Title    string `json:"title"`
		Package  string `json:"module_name"`
		Severity string `json:"severity"`
		Patched  string `json:"patched_versions"`
		URL      string `json:"url"`
		Findings []struct {
			Version string `json:"version"`
		} `json:"findings"`
	} `json:"advisories"`
	Metadata *struct {
		Vulnerabilities map[string]int `json:"vulnerabilities"`
	} `json:"metadata"`
	Error json.RawMessage `json:"error"`
}

func parsePnpmAudit(data []byte) ([]*builderv0.AuditFinding, error) {
	var report pnpmAuditReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse pnpm audit JSON: %w", err)
	}
	if len(report.Error) > 0 && string(report.Error) != "null" {
		return nil, fmt.Errorf("pnpm registry reported an error: %s", report.Error)
	}
	if report.Advisories == nil || report.Metadata == nil || report.Metadata.Vulnerabilities == nil {
		return nil, fmt.Errorf("pnpm audit returned no complete advisory report")
	}
	var findings []*builderv0.AuditFinding
	for key, advisory := range report.Advisories {
		severity := npmSeverity(advisory.Severity)
		if advisory.Package == "" || len(advisory.Findings) == 0 || severity == builderv0.AuditFinding_UNKNOWN {
			return nil, fmt.Errorf("pnpm advisory %s is incomplete", key)
		}
		id := advisory.GithubID
		if id == "" {
			id = strconv.Itoa(advisory.ID)
		}
		if id == "0" {
			return nil, fmt.Errorf("pnpm advisory %s has no identity", key)
		}
		seen := map[string]bool{}
		for _, finding := range advisory.Findings {
			if finding.Version == "" {
				return nil, fmt.Errorf("pnpm advisory %s has no affected version", key)
			}
			if seen[finding.Version] {
				continue
			}
			seen[finding.Version] = true
			findings = append(findings, &builderv0.AuditFinding{
				Id: id, Package: advisory.Package, CurrentVersion: finding.Version,
				FixedVersion: advisory.Patched, Severity: severity, Summary: advisory.Title, Url: advisory.URL,
			})
		}
	}
	total := 0
	for _, count := range report.Metadata.Vulnerabilities {
		total += count
	}
	if total > 0 && len(findings) == 0 {
		return nil, fmt.Errorf("pnpm audit counted vulnerabilities but returned no advisories")
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Id != b.Id {
			return a.Id < b.Id
		}
		return a.CurrentVersion < b.CurrentVersion
	})
	return findings, nil
}

// pnpm outdated may omit current when node_modules is absent. Resolve current
// from pnpm's lockfile-only listing, never from an ambient installation.
func pnpmOutdated(ctx context.Context, dir string, includeDev bool) ([]*builderv0.OutdatedDep, error) {
	listArgs := []string{"list", "--lockfile-only", "--depth", "0", "--json"}
	outdatedArgs := []string{"outdated", "--json"}
	if !includeDev {
		listArgs = append(listArgs, "--prod")
		outdatedArgs = append(outdatedArgs, "--prod")
	}
	locked, err := runCmd(ctx, dir, "pnpm", listArgs...)
	if err != nil {
		return nil, fmt.Errorf("pnpm locked dependency list: %w", err)
	}
	output, runErr := runCmd(ctx, dir, "pnpm", outdatedArgs...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	outdated, err := parsePnpmOutdated(locked, output)
	if err != nil {
		return nil, errors.Join(err, runErr)
	}
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("pnpm outdated: %w", runErr)
		}
	}
	return outdated, nil
}

func parsePnpmOutdated(locked, output []byte) ([]*builderv0.OutdatedDep, error) {
	type dependency struct {
		Version string `json:"version"`
	}
	var projects []struct {
		Dependencies         map[string]dependency `json:"dependencies"`
		DevDependencies      map[string]dependency `json:"devDependencies"`
		OptionalDependencies map[string]dependency `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(locked, &projects); err != nil {
		return nil, fmt.Errorf("parse pnpm locked dependencies: %w", err)
	}
	if len(projects) != 1 {
		return nil, fmt.Errorf("pnpm outdated requires exactly one selected project")
	}
	current := map[string]string{}
	for _, group := range []map[string]dependency{projects[0].Dependencies, projects[0].DevDependencies, projects[0].OptionalDependencies} {
		for name, item := range group {
			current[name] = item.Version
		}
	}
	var entries map[string]npmOutdatedEntry
	if err := json.Unmarshal(pnpmJSONReport(output), &entries); err != nil {
		return nil, fmt.Errorf("parse pnpm outdated: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("pnpm outdated returned no report")
	}
	var result []*builderv0.OutdatedDep
	for name, entry := range entries {
		if entry.Wanted == "" && entry.Latest == "" {
			return nil, fmt.Errorf("pnpm outdated entry %s has no version evidence", name)
		}
		version := current[name]
		if version == "" {
			return nil, fmt.Errorf("pnpm outdated package %s is absent from the selected lock", name)
		}
		// Non-registry/tarball dependencies have no latest registry version.
		if entry.Latest == "" || entry.Latest == version {
			continue
		}
		result = append(result, &builderv0.OutdatedDep{Package: name, Current: version, LatestSafe: entry.Wanted, LatestMajor: entry.Latest})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Package < result[j].Package })
	return result, nil
}

// pnpmJSONReport returns the JSON document in a pnpm --json report. pnpm
// writes its diagnostics ("WARN  GET https://registry.npmjs.org/… error
// (ECONNRESET)", a deprecated-setting notice) to stdout BEFORE the report,
// so a report that is otherwise whole begins with lines that are not JSON.
// Those lines are pnpm talking about the fetch, not evidence about any
// dependency; the report starts at the first line that opens a JSON value,
// and everything before it is dropped. A report with no such line is
// returned unchanged, so the parse error names what pnpm actually printed.
func pnpmJSONReport(output []byte) []byte {
	rest := output
	for len(rest) > 0 {
		line, tail, _ := bytes.Cut(rest, []byte("\n"))
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			return rest
		}
		rest = tail
	}
	return output
}
