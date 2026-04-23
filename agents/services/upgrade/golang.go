package upgrade

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Golang bumps Go module dependencies in dir.
//
// Strategy:
//   - patch+minor (default): `go get -u=patch ./...` for individual deps
//     in the main module; this is the conservative semver-safe bump.
//   - include_major: `go get -u ./...` (still respects semantic import
//     versioning — module v2+ requires `/v2` in the path, so true major
//     bumps need explicit re-import; this just picks up the latest minor
//     within the current major).
//
// Then `go mod tidy` to align go.sum. Changes are computed by snapshotting
// `go list -m -json all` before and after.
//
// dry_run snapshots before, runs the bump in a copy via go.work overlay
// — too invasive — so for now we just compute the bump preview by
// reading `go list -m -u -json all` (which knows the available updates)
// and skip the write.
func Golang(ctx context.Context, dir string, opts Options) (*Result, error) {
	if opts.DryRun {
		return golangDryRun(ctx, dir, opts)
	}

	before, err := snapshotMods(ctx, dir)
	if err != nil {
		return nil, err
	}

	args := []string{"get"}
	if opts.IncludeMajor {
		args = append(args, "-u")
	} else {
		args = append(args, "-u=patch")
	}
	if len(opts.Only) > 0 {
		args = append(args, opts.Only...)
	} else {
		args = append(args, "./...")
	}
	if _, err := runCmd(ctx, dir, "go", args...); err != nil {
		// Surface the tool output via the diff field so the CLI can show
		// what failed; bubble the err so the agent reports ERROR.
		return nil, err
	}
	if _, err := runCmd(ctx, dir, "go", "mod", "tidy"); err != nil {
		return nil, err
	}

	after, err := snapshotMods(ctx, dir)
	if err != nil {
		return nil, err
	}

	return &Result{
		Changes:      diffMods(before, after),
		LockfileDiff: gitDiffShortstat(ctx, dir, "go.mod", "go.sum"),
	}, nil
}

func golangDryRun(ctx context.Context, dir string, opts Options) (*Result, error) {
	out, _ := runCmd(ctx, dir, "go", "list", "-m", "-u", "-json", "all")
	var changes []*builderv0.UpgradeChange
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m goListEntry
		if err := dec.Decode(&m); err != nil {
			continue
		}
		if m.Update == nil || m.Indirect {
			continue
		}
		// IncludeMajor doesn't change what `go list -u` returns (it always
		// reports the next available within the same major); honor opts.Only
		// here since dry-run is also expected to filter.
		if !inOnly(m.Path, opts.Only) {
			continue
		}
		changes = append(changes, &builderv0.UpgradeChange{
			Package: m.Path,
			From:    m.Version,
			To:      m.Update.Version,
		})
	}
	return &Result{Changes: changes}, nil
}

type goListEntry struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Update   *struct {
		Version string `json:"Version"`
	} `json:"Update,omitempty"`
	Indirect bool `json:"Indirect,omitempty"`
	Main     bool `json:"Main,omitempty"`
}

func snapshotMods(ctx context.Context, dir string) (map[string]string, error) {
	out, err := runCmd(ctx, dir, "go", "list", "-m", "-json", "all")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	snap := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m goListEntry
		if err := dec.Decode(&m); err != nil {
			continue
		}
		if m.Main {
			continue // skip the main module itself
		}
		snap[m.Path] = m.Version
	}
	return snap, nil
}

func diffMods(before, after map[string]string) []*builderv0.UpgradeChange {
	var changes []*builderv0.UpgradeChange
	for path, newV := range after {
		if oldV := before[path]; oldV != newV {
			changes = append(changes, &builderv0.UpgradeChange{
				Package: path,
				From:    oldV,
				To:      newV,
			})
		}
	}
	return changes
}

func inOnly(name string, only []string) bool {
	if len(only) == 0 {
		return true
	}
	return slices.Contains(only, name)
}
