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

	before, err := snapshotNpm(ctx, dir)
	if err != nil {
		return nil, err
	}

	if opts.IncludeMajor {
		// Per-pkg install from outdated list. We resolve the list first,
		// then install each at @latest. `npm outdated` exits non-zero when
		// it *finds* outdated packages — that's not a run failure, so only
		// bubble the error when there's no JSON to parse.
		out, err := runCmd(ctx, dir, "npm", "outdated", "--json")
		if err != nil && len(out) == 0 {
			return nil, err
		}
		var raw map[string]struct{ Latest string }
		if len(out) > 0 {
			if err := json.Unmarshal(out, &raw); err != nil {
				return nil, err
			}
		}
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

	after, err := snapshotNpm(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &Result{
		Changes:      diffMods(before, after),
		LockfileDiff: gitDiffShortstat(ctx, dir, "package.json", "package-lock.json"),
	}, nil
}

func nodeDryRun(ctx context.Context, dir string, opts Options) (*Result, error) {
	// `npm outdated` exits non-zero when it *finds* outdated packages — that
	// is a finding, not a run failure, and it still writes valid JSON. Only
	// surface the error when there's no output to parse (binary missing, etc).
	out, err := runCmd(ctx, dir, "npm", "outdated", "--json")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	if len(out) == 0 {
		return &Result{}, nil
	}
	var raw map[string]struct {
		Current string `json:"current"`
		Wanted  string `json:"wanted"`
		Latest  string `json:"latest"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
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
// `npm ls` exits non-zero on dependency-tree warnings while still printing
// valid JSON, so only the no-output case is treated as a run failure.
func snapshotNpm(ctx context.Context, dir string) (map[string]string, error) {
	out, err := runCmd(ctx, dir, "npm", "ls", "--json", "--depth=0")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	if len(out) == 0 {
		return map[string]string{}, nil
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
	return snap, nil
}
