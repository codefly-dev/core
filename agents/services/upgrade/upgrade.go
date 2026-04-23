// Package upgrade applies dependency bumps on behalf of codefly Builder
// agents. Each language has an Apply(ctx, dir, opts) entry point that
// invokes the canonical "update lockfile" command and reports what
// changed.
//
// Defaults are semver-safe (patch+minor only). IncludeMajor opts in to
// breaking bumps. DryRun reports the changes without writing the
// lockfile (where the upstream tool supports it).
package upgrade

import (
	"bytes"
	"context"
	"os/exec"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Options is shared across language adapters.
type Options struct {
	IncludeMajor bool
	DryRun       bool
	// Only restricts the upgrade to these packages. Empty = all eligible.
	Only []string
}

// Result is what every Apply* function returns. LockfileDiff is a
// human-facing summary (e.g. truncated `git diff` of go.sum) for the
// CLI to print; it's allowed to be empty.
type Result struct {
	Changes      []*builderv0.UpgradeChange
	LockfileDiff string
}

func runCmd(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	return out.Bytes(), err
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// gitDiffShortstat returns `git diff --stat <files>` for the upgrade's
// lockfile(s), used as the human-facing LockfileDiff. Best-effort —
// empty string if not in a git repo.
func gitDiffShortstat(ctx context.Context, dir string, paths ...string) string {
	if !have("git") {
		return ""
	}
	args := append([]string{"diff", "--stat", "--"}, paths...)
	out, _ := runCmd(ctx, dir, "git", args...)
	return string(out)
}
