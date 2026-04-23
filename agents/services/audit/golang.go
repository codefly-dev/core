package audit

import (
	"context"
	"encoding/json"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Golang scans a Go module rooted at dir.
//
// Vulnerabilities: govulncheck -json ./...  (callgraph-aware; only
// reports vulns the binary can actually reach). Outdated: go list -m
// -u -json all (returns each module with its current + Update fields).
//
// Both tools are skipped (Tool="missing") if not on PATH so the agent
// scan still completes — the CLI surfaces the missing-tool warning.
func Golang(ctx context.Context, dir string, includeOutdated bool) (*Result, error) {
	res := &Result{Language: "GO", Tool: "go list -u"}
	tools := []string{}

	if have("govulncheck") {
		tools = append(tools, "govulncheck")
		findings, err := runGovulncheck(ctx, dir)
		if err != nil {
			return nil, err
		}
		res.Findings = findings
	}

	if includeOutdated && have("go") {
		outdated, err := runGoListUpdates(ctx, dir)
		if err != nil {
			return nil, err
		}
		res.Outdated = outdated
	}

	if len(tools) == 0 && !have("go") {
		res.Tool = "missing"
	} else if len(tools) > 0 {
		res.Tool = strings.Join(append(tools, "go list -u"), "+")
	}
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
			Module  string `json:"module"`
			Version string `json:"version"`
			Package string `json:"package"`
		} `json:"trace"`
	} `json:"finding,omitempty"`
	OSV *struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Aliases []string `json:"aliases"`
	} `json:"osv,omitempty"`
}

func runGovulncheck(ctx context.Context, dir string) ([]*builderv0.AuditFinding, error) {
	out, _ := runCmd(ctx, dir, "govulncheck", "-json", "./...")
	// govulncheck exits non-zero when findings exist; we still parse.
	return runGovulncheckParseBytes(out)
}

// runGovulncheckParseBytes is the pure JSON-parsing path, exposed for
// testing without invoking the binary.
func runGovulncheckParseBytes(out []byte) ([]*builderv0.AuditFinding, error) {
	// Build OSV id → summary lookup as we stream.
	summaries := map[string]string{}
	var findings []*builderv0.AuditFinding

	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var msg govulncheckOutput
		if err := dec.Decode(&msg); err != nil {
			// Malformed JSON line — skip rather than fail the whole scan.
			continue
		}
		if msg.OSV != nil {
			summaries[msg.OSV.ID] = msg.OSV.Summary
		}
		if msg.Finding != nil && len(msg.Finding.Trace) > 0 {
			t := msg.Finding.Trace[0]
			findings = append(findings, &builderv0.AuditFinding{
				Severity:       builderv0.AuditFinding_HIGH, // govulncheck only reports actually-reachable vulns
				Id:             msg.Finding.OSV,
				Package:        t.Module,
				CurrentVersion: t.Version,
				FixedVersion:   msg.Finding.FixedVersion,
				Summary:        summaries[msg.Finding.OSV],
				Url:            "https://pkg.go.dev/vuln/" + msg.Finding.OSV,
			})
		}
	}
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
			continue
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
