package upgrade

import (
	"context"
	"encoding/json"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Node bumps Node packages in dir.
//
// Strategy:
//   - default (patch+minor): `npm update --save` — respects the semver
//     range in package.json (caret/tilde), so it picks up the latest
//     within the allowed range.
//   - include_major: `npm install <pkg>@latest` per package, computed
//     from `npm outdated --json`. This crosses major boundaries.
//
// Changes are computed from the diff of `npm ls --json` snapshots.
// dry_run uses `npm outdated --json` (no writes).
func Node(ctx context.Context, dir string, opts Options) (*Result, error) {
	if !have("npm") {
		return &Result{}, nil
	}
	if opts.DryRun {
		return nodeDryRun(ctx, dir, opts)
	}

	before := snapshotNpm(ctx, dir)

	if opts.IncludeMajor {
		// Per-pkg install from outdated list. We resolve the list first,
		// then install each at @latest.
		out, _ := runCmd(ctx, dir, "npm", "outdated", "--json")
		var raw map[string]struct{ Latest string }
		_ = json.Unmarshal(out, &raw)
		for name := range raw {
			if !inOnly(name, opts.Only) {
				continue
			}
			if _, err := runCmd(ctx, dir, "npm", "install", name+"@latest"); err != nil {
				return nil, err
			}
		}
	} else {
		args := []string{"update", "--save"}
		if len(opts.Only) > 0 {
			args = append(args, opts.Only...)
		}
		if _, err := runCmd(ctx, dir, "npm", args...); err != nil {
			return nil, err
		}
	}

	after := snapshotNpm(ctx, dir)
	return &Result{
		Changes:      diffMods(before, after),
		LockfileDiff: gitDiffShortstat(ctx, dir, "package.json", "package-lock.json"),
	}, nil
}

func nodeDryRun(ctx context.Context, dir string, opts Options) (*Result, error) {
	out, _ := runCmd(ctx, dir, "npm", "outdated", "--json")
	if len(out) == 0 {
		return &Result{}, nil
	}
	var raw map[string]struct {
		Current string `json:"current"`
		Wanted  string `json:"wanted"`
		Latest  string `json:"latest"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return &Result{}, nil
	}
	var changes []*builderv0.UpgradeChange
	for name, e := range raw {
		if !inOnly(name, opts.Only) {
			continue
		}
		to := e.Wanted
		if opts.IncludeMajor {
			to = e.Latest
		}
		if to == "" || to == e.Current {
			continue
		}
		changes = append(changes, &builderv0.UpgradeChange{
			Package: name, From: e.Current, To: to,
		})
	}
	return &Result{Changes: changes}, nil
}

// snapshotNpm returns {package: version} from `npm ls --json --depth=0`.
// Best-effort — empty map on failure.
func snapshotNpm(ctx context.Context, dir string) map[string]string {
	out, _ := runCmd(ctx, dir, "npm", "ls", "--json", "--depth=0")
	if len(out) == 0 {
		return map[string]string{}
	}
	// npm ls schema: { "dependencies": { "name": { "version": "x.y.z" } } }
	var ls struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	// npm prints warnings to stdout above the JSON; recover by slicing.
	if err := json.Unmarshal(out, &ls); err != nil {
		if i := strings.IndexByte(string(out), '{'); i >= 0 {
			_ = json.Unmarshal(out[i:], &ls)
		}
	}
	snap := map[string]string{}
	for name, d := range ls.Dependencies {
		snap[name] = d.Version
	}
	return snap
}
