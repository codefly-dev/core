package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// GovulncheckVersion is the scanner revision Codefly resolves when no
// operator-provided binary is present. Pinning it makes release evidence
// reproducible and avoids the old host-PATH-dependent silent skip.
const GovulncheckVersion = "v1.6.0"

// Golang scans a Go module rooted at dir.
//
// Vulnerabilities: govulncheck -json ./...  (callgraph-aware; only
// reports vulns the binary can actually reach). Outdated: go list -m
// -u -json all (returns each module with its current + Update fields).
//
// A PATH-installed govulncheck is honored for hermetic/operator-managed
// environments. Otherwise Codefly runs the pinned scanner through the Go tool,
// which uses Go's content-addressed module/build cache without modifying the
// audited module. If neither path is possible the audit fails explicitly.
func Golang(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "GO"}
	findings, scanner, err := runGovulncheck(ctx, dir)
	if err != nil {
		return nil, err
	}
	res.Findings = findings
	tools := []string{scanner}

	if includeOutdated && have("go") {
		tools = append(tools, "go list -u")
		outdated, err := runGoListUpdates(ctx, dir)
		if err != nil {
			return nil, err
		}
		res.Outdated = outdated
	}

	res.Tool = strings.Join(tools, "+")
	return res, nil
}

// govulncheckOutput streams JSON objects (one per line). We only care
// about the "finding" objects which contain CVE id + severity + the
// affected module/version. The schema is documented at
// https://pkg.go.dev/golang.org/x/vuln/internal/govulncheck.
type govulncheckOutput struct {
	Finding *struct {
		OSV          string `json:"osv"`
		FixedVersion string `json:"fixed_version"`
		Trace        []struct {
			Module   string `json:"module"`
			Version  string `json:"version"`
			Package  string `json:"package"`
			Function string `json:"function"`
		} `json:"trace"`
	} `json:"finding,omitempty"`
	OSV *struct {
		ID      string   `json:"id"`
		Summary string   `json:"summary"`
		Aliases []string `json:"aliases"`
	} `json:"osv,omitempty"`
}

func runGovulncheck(ctx context.Context, dir string) ([]*builderv0.AuditFinding, string, error) {
	name := "govulncheck"
	args := []string{"-json", "./..."}
	tool := "govulncheck"
	if !have(name) {
		if !have("go") {
			return nil, "", fmt.Errorf("govulncheck unavailable: neither govulncheck nor go is installed")
		}
		name = "go"
		args = []string{"run", "golang.org/x/vuln/cmd/govulncheck@" + GovulncheckVersion, "-json", "./..."}
		tool = "govulncheck@" + GovulncheckVersion
	}
	out, err := runCmd(ctx, dir, name, args...)
	// govulncheck exits non-zero when findings exist; we still parse.
	// But a genuine run failure (module not initialized, binary errors)
	// produces no output to parse — propagate it instead of masking the
	// scan failure as "no vulnerabilities". Mirrors runGoListUpdates.
	if err != nil && len(out) == 0 {
		return nil, "", err
	}
	findings, parseErr := runGovulncheckParseBytes(out)
	return findings, tool, parseErr
}

// runGovulncheckParseBytes is the pure JSON-parsing path, exposed for
// testing without invoking the binary.
func runGovulncheckParseBytes(out []byte) ([]*builderv0.AuditFinding, error) {
	// Build OSV id → summary lookup as we stream.
	summaries := map[string]string{}
	// govulncheck emits one finding per reachable call trace. A single
	// advisory can therefore appear hundreds of times for the same affected
	// module. Release evidence is advisory/module based, not call-path based.
	findingsByKey := map[string]*builderv0.AuditFinding{}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var msg govulncheckOutput
		if err := dec.Decode(&msg); err != nil {
			// json.Decoder does NOT advance past a syntax error, so every
			// subsequent Decode returns the same error and More() stays true —
			// `continue` here spun the agent at 100% CPU forever. Stop at the
			// first malformed token instead.
			break
		}
		if msg.OSV != nil {
			summaries[msg.OSV.ID] = msg.OSV.Summary
		}
		if msg.Finding != nil && len(msg.Finding.Trace) > 0 {
			t := msg.Finding.Trace[0]
			// Symbol-mode govulncheck emits module-, package-, and symbol-level
			// findings as it progressively analyzes the graph. Only a frame
			// with a function is a reachable vulnerability; module/package
			// frames are inventory evidence and are reported separately by
			// govulncheck as "not called".
			if t.Function == "" {
				continue
			}
			key := msg.Finding.OSV + "\x00" + t.Module + "\x00" + t.Version
			finding, exists := findingsByKey[key]
			if !exists {
				finding = &builderv0.AuditFinding{
					Severity:       builderv0.AuditFinding_HIGH, // govulncheck only reports actually-reachable vulns
					Id:             msg.Finding.OSV,
					Package:        t.Module,
					CurrentVersion: t.Version,
					Url:            "https://pkg.go.dev/vuln/" + msg.Finding.OSV,
				}
				findingsByKey[key] = finding
			}
			if finding.FixedVersion == "" && msg.Finding.FixedVersion != "" {
				finding.FixedVersion = msg.Finding.FixedVersion
			}
		}
	}
	findings := make([]*builderv0.AuditFinding, 0, len(findingsByKey))
	for _, finding := range findingsByKey {
		finding.Summary = summaries[finding.Id]
		findings = append(findings, finding)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].GetId() != findings[j].GetId() {
			return findings[i].GetId() < findings[j].GetId()
		}
		if findings[i].GetPackage() != findings[j].GetPackage() {
			return findings[i].GetPackage() < findings[j].GetPackage()
		}
		return findings[i].GetCurrentVersion() < findings[j].GetCurrentVersion()
	})
	return findings, nil
}

// goListUpdate is the json shape of `go list -m -u -json all` per module.
type goListUpdate struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Update  *struct {
		Version string `json:"Version"`
	} `json:"Update,omitempty"`
	Indirect bool `json:"Indirect,omitempty"`
}

func runGoListUpdates(ctx context.Context, dir string) ([]*builderv0.OutdatedDep, error) {
	out, err := runCmd(ctx, dir, "go", "list", "-m", "-u", "-json", "all")
	if err != nil && len(out) == 0 {
		// `go list` failed completely; nothing to parse.
		return nil, nil
	}
	return parseGoListUpdates(out)
}

// parseGoListUpdates is the pure JSON-parsing path, exposed for testing.
func parseGoListUpdates(out []byte) ([]*builderv0.OutdatedDep, error) {
	var outdated []*builderv0.OutdatedDep
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m goListUpdate
		if err := dec.Decode(&m); err != nil {
			// Decoder cannot advance past a syntax error: break, never
			// continue (which would spin forever). See parseGovulncheck.
			break
		}
		if m.Update == nil || m.Indirect {
			continue
		}
		outdated = append(outdated, &builderv0.OutdatedDep{
			Package: m.Path,
			Current: m.Version,
			// go list -u doesn't distinguish patch/minor vs major; treat
			// the available update as "latest_safe" — true for most modules
			// since Go modules use semantic import versioning for major bumps.
			LatestSafe: m.Update.Version,
		})
	}
	return outdated, nil
}
