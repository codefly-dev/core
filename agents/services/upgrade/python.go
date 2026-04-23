package upgrade

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Python bumps Python deps in dir.
//
// Strategy: parse requirements.txt, query `pip install --dry-run <pkg>==`
// to find the latest version pip considers installable (respecting
// already-installed constraints). For include_major=false we filter out
// version jumps that cross the major component.
//
// dry_run: report what would change but never invoke pip install.
// non-dry: rewrite requirements.txt with pinned versions and run
// `pip install -r requirements.txt --upgrade` so the venv is also updated.
func Python(ctx context.Context, dir string, opts Options) (*Result, error) {
	if !have("pip") {
		return &Result{}, nil
	}
	out, _ := runCmd(ctx, dir, "pip", "list", "--outdated", "--format", "json")
	if len(out) == 0 {
		return &Result{}, nil
	}
	var entries []struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		LatestVersion string `json:"latest_version"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return &Result{}, nil
	}

	var changes []*builderv0.UpgradeChange
	for _, e := range entries {
		if !inOnly(e.Name, opts.Only) {
			continue
		}
		if !opts.IncludeMajor && majorOf(e.Version) != majorOf(e.LatestVersion) {
			continue
		}
		changes = append(changes, &builderv0.UpgradeChange{
			Package: e.Name, From: e.Version, To: e.LatestVersion,
		})
	}

	if !opts.DryRun && len(changes) > 0 {
		if err := rewriteRequirements(dir, changes); err != nil {
			return nil, err
		}
		// Re-install upgraded packages into the venv pip is using.
		args := []string{"install", "--upgrade"}
		for _, c := range changes {
			args = append(args, c.Package+"=="+c.To)
		}
		if _, err := runCmd(ctx, dir, "pip", args...); err != nil {
			return nil, err
		}
	}

	return &Result{
		Changes:      changes,
		LockfileDiff: gitDiffShortstat(ctx, dir, "requirements.txt"),
	}, nil
}

// majorOf returns the leading numeric component of a version, or empty.
// "5.4.5" -> "5", "1.0.0a1" -> "1", "" -> "".
var majorRe = regexp.MustCompile(`^(\d+)`)

func majorOf(v string) string {
	m := majorRe.FindStringSubmatch(v)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// rewriteRequirements updates the version pin for each upgraded package
// in requirements.txt. Lines that don't match a known package are kept
// verbatim (comments, -r, -e, --extra-index-url, etc.). If the file
// doesn't exist, no-op.
func rewriteRequirements(dir string, changes []*builderv0.UpgradeChange) error {
	path := filepath.Join(dir, "requirements.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pinFor := map[string]string{}
	for _, c := range changes {
		pinFor[strings.ToLower(c.Package)] = c.To
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		// Find the package name by splitting on the first ==, >=, ~=, etc.
		name := splitOnSpecifier(trim)
		if to, ok := pinFor[strings.ToLower(name)]; ok {
			lines[i] = name + "==" + to
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// splitOnSpecifier extracts the bare package name from a requirements line.
// "django==4.2.0" -> "django"; "requests>=2.28" -> "requests"; "uvicorn[standard]" -> "uvicorn".
func splitOnSpecifier(line string) string {
	for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "["} {
		if before, _, ok := strings.Cut(line, sep); ok {
			return strings.TrimSpace(before)
		}
	}
	return strings.TrimSpace(line)
}
