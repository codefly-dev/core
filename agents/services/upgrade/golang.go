package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
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
// A package named in Only that the module does not require yet is ADDED, at
// its latest version or at the version it names (`pkg@v1.2.3`): `go get
// -u=patch` refuses a module with no required version ("can't query version
// patch ... no existing version is required"), and that refusal left a
// service with no way to gain a dependency through its agent. `go mod tidy`
// keeps a requirement only while the source imports it, so the import comes
// first; an added package that no source imports is reported as an error
// rather than as a silent no-op.
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

	commands, added := goGetCommands(opts, before)
	for _, args := range commands {
		// Surface the tool output via the diff field so the CLI can show
		// what failed; bubble the err so the agent reports ERROR.
		if _, err = runCmd(ctx, dir, "go", args...); err != nil {
			return nil, err
		}
	}
	if _, err = runCmd(ctx, dir, "go", "mod", "tidy"); err != nil {
		return nil, err
	}

	after, err := snapshotMods(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, pkg := range added {
		if !requires(after, pkg) {
			return nil, fmt.Errorf("%s was added, but nothing in the module imports it, so `go mod tidy` removed it again: import it in the source first, then upgrade", pkg)
		}
	}

	return &Result{
		Changes:      diffMods(before, after),
		LockfileDiff: gitDiffShortstat(ctx, dir, "go.mod", "go.sum"),
	}, nil
}

// goGetCommands is the `go get` invocations of one upgrade, and the packages
// among Only that it adds rather than bumps.
//
// With no Only, every dependency of the main module is bumped. With one, the
// packages the module already requires are bumped together under the semver
// flag, and the rest — a package of a module not required yet, or any package
// naming its own version query (`pkg@v1.2.3`, `pkg@latest`) — are fetched
// without that flag, because `-u=patch` has no required version to patch from
// and an explicit query already says which version is wanted.
func goGetCommands(opts Options, required map[string]string) (commands [][]string, added []string) {
	flag := "-u=patch"
	if opts.IncludeMajor {
		flag = "-u"
	}
	const get = "get"
	if len(opts.Only) == 0 {
		return [][]string{{get, flag, "./..."}}, nil
	}
	var bump, fetch []string
	for _, pkg := range opts.Only {
		path, _, pinned := strings.Cut(pkg, "@")
		switch {
		case !requires(required, path):
			fetch = append(fetch, pkg)
			added = append(added, path)
		case pinned:
			fetch = append(fetch, pkg)
		default:
			bump = append(bump, pkg)
		}
	}
	if len(bump) > 0 {
		commands = append(commands, append([]string{get, flag}, bump...))
	}
	if len(fetch) > 0 {
		commands = append(commands, append([]string{get}, fetch...))
	}
	return commands, added
}

// requires reports whether a module providing pkg is in the snapshot: pkg is
// a required module's path, or a package inside one. A major version is its
// own module (semantic import versioning): example.com/m/v5 is not a package
// of example.com/m, so requiring one says nothing about the other.
func requires(mods map[string]string, pkg string) bool {
	for path := range mods {
		if pkg == path {
			return true
		}
		rest, inside := strings.CutPrefix(pkg, path+"/")
		if !inside {
			continue
		}
		first, _, _ := strings.Cut(rest, "/")
		if !isMajorVersionElement(first) {
			return true
		}
	}
	return false
}

// isMajorVersionElement reports whether a path element is a module's major
// version suffix: v2, v3, ... (never v0 or v1, which have no suffix).
func isMajorVersionElement(element string) bool {
	if len(element) < 2 || element[0] != 'v' || element[1] == '0' || element == "v1" {
		return false
	}
	for _, r := range element[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func golangDryRun(ctx context.Context, dir string, opts Options) (*Result, error) {
	out, err := runCmd(ctx, dir, "go", "list", "-m", "-u", "-json", "all")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var changes []*builderv0.UpgradeChange
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m goListEntry
		if err := dec.Decode(&m); err != nil {
			// Decoder cannot advance past a syntax error — break, never
			// continue, or the agent spins at 100% CPU forever.
			break
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
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Update  *struct {
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
			// Decoder cannot advance past a syntax error — break, never continue.
			break
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
