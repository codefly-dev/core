// Package audit runs language-specific dependency audits on behalf of
// codefly Builder agents. Each language has a Scan(ctx, dir) entry
// point that shells out to the canonical CVE scanner + outdated-dep
// reporter for that ecosystem and returns structured findings.
//
// The package never fails the agent if a tool is missing — it returns
// Tool="missing" with empty findings so the CLI can render
// "[missing govulncheck] go-grpc/api: skipped" instead of erroring.
package audit

import (
	"bytes"
	"context"
	"os/exec"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Result is the language-agnostic shape returned by every Scan*
// function. Tool identifies what produced it ("govulncheck+go-list-u",
// "npm-audit+outdated", "pip-audit", "trivy", or "missing").
type Result struct {
	Findings []*builderv0.AuditFinding
	Outdated []*builderv0.OutdatedDep
	Tool     string
	Language string
}

// runCmd executes cmd in dir and returns combined stdout/stderr.
// Returns the output even on non-zero exit so JSON-emitting tools
// (govulncheck, npm audit) that exit non-zero on findings still
// surface their report.
func runCmd(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	return out.Bytes(), err
}

// have returns true if the named tool is on PATH. Used by each language
// adapter to fall back to Tool="missing" gracefully instead of bubbling
// exec.ErrNotFound out of the agent.
func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
