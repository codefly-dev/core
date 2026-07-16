// Package audit runs language-specific dependency audits on behalf of
// codefly Builder agents. Each language has a Scan(ctx, dir) entry
// point that shells out to the canonical CVE scanner + outdated-dep
// reporter for that ecosystem and returns structured findings.
//
// Required scanners fail explicitly when they cannot run. A missing scanner
// must never be represented as a successful empty result: release gates need
// to distinguish "clean" from "not scanned".
package audit

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Result is the language-agnostic shape returned by every Scan*
// function. Tool identifies what produced it ("govulncheck+go-list-u",
// "npm-audit+outdated", "uv-export+pip-audit", "osv-scanner", or "trivy").
type Result struct {
	Findings []*builderv0.AuditFinding
	Outdated []*builderv0.OutdatedDep
	Tool     string
	Language string
}

// runCmd executes cmd in dir and returns its STDOUT (only). stderr is captured
// separately and folded into the returned error, never into the returned bytes:
// these tools emit their JSON report on stdout, and progress noise like
// `go: downloading ...` on stderr. Merging the two streams put non-JSON bytes
// mid-report, which json.Decoder cannot skip past (the historical 100%-CPU
// hang). Output is returned even on non-zero exit so JSON-emitting tools that
// exit non-zero on findings (govulncheck, npm audit) still surface their report.
func runCmd(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), err
}

// have returns true if the named tool is on PATH. Used by each language
// adapter to fall back to Tool="missing" gracefully instead of bubbling
// exec.ErrNotFound out of the agent.
func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
