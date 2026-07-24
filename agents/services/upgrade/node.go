package upgrade

import (
	"context"
	"encoding/json"
	"path"
	"sort"
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
	out, err := runCmd(ctx, dir, "npm", "outdated", "--json", "--depth=0")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	raw, err := parseNpmOutdated(out)
	if err != nil {
		return nil, err
	}
	workspaces, err := npmWorkspaceNames(ctx, dir)
	if err != nil {
		return nil, err
	}
	plannedChanges := npmUpgradeChanges(raw, opts, workspaces)

	if opts.IncludeMajor {
		// Per-pkg install from outdated list. We resolve the list first,
		// then install each at @latest. `npm outdated` exits non-zero when
		// it *finds* outdated packages — that's not a run failure, so only
		// bubble the error when there's no JSON to parse.
		for _, group := range npmLatestInstallGroups(raw, opts.Only, workspaces) {
			args := append([]string{"install"}, group.Packages...)
			if group.Workspace != "" {
				args = append(args, "--workspace", group.Workspace)
			}
			if _, err := runCmd(ctx, dir, "npm", args...); err != nil {
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
		Changes:      mergeUpgradeChanges(diffMods(before, after), plannedChanges),
		LockfileDiff: gitDiffShortstat(ctx, dir, "package.json", "package-lock.json"),
	}, nil
}

func nodeDryRun(ctx context.Context, dir string, opts Options) (*Result, error) {
	// `npm outdated` exits non-zero when it *finds* outdated packages — that
	// is a finding, not a run failure, and it still writes valid JSON. Only
	// surface the error when there's no output to parse (binary missing, etc).
	out, err := runCmd(ctx, dir, "npm", "outdated", "--json", "--depth=0")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	if len(out) == 0 {
		return &Result{}, nil
	}
	raw, err := parseNpmOutdated(out)
	if err != nil {
		return nil, err
	}
	workspaces, err := npmWorkspaceNames(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &Result{Changes: npmUpgradeChanges(raw, opts, workspaces)}, nil
}

func npmUpgradeChanges(entries map[string][]npmOutdatedEntry, opts Options, workspaces map[string]string) []*builderv0.UpgradeChange {
	var changes []*builderv0.UpgradeChange
	for name, candidates := range entries {
		if !inOnly(name, opts.Only) {
			continue
		}
		e, to, ok := npmOutdatedChange(candidates, opts.IncludeMajor, workspaces)
		if !ok {
			continue
		}
		changes = append(changes, &builderv0.UpgradeChange{
			Package: name, From: e.Current, To: to,
		})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Package < changes[j].Package })
	return changes
}

// npm ls only snapshots root dependencies. Preserve successful workspace-only
// changes from the pre-install outdated plan while preferring observed root
// versions whenever both sources report the same package.
func mergeUpgradeChanges(observed, planned []*builderv0.UpgradeChange) []*builderv0.UpgradeChange {
	merged := make(map[string]*builderv0.UpgradeChange, len(observed)+len(planned))
	for _, change := range planned {
		merged[change.Package] = change
	}
	for _, change := range observed {
		merged[change.Package] = change
	}
	changes := make([]*builderv0.UpgradeChange, 0, len(merged))
	for _, change := range merged {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Package < changes[j].Package })
	return changes
}

type npmOutdatedEntry struct {
	Current   string `json:"current"`
	Wanted    string `json:"wanted"`
	Latest    string `json:"latest"`
	Dependent string `json:"dependent"`
}

// npm 11 returns either one object per package or an array when multiple
// workspaces depend on the same package. `--depth=0` keeps those arrays scoped
// to direct workspace dependencies; the first entry is the root workspace when
// present and therefore reflects the range that an `npm update` will honor.
func parseNpmOutdated(out []byte) (map[string][]npmOutdatedEntry, error) {
	entries := map[string][]npmOutdatedEntry{}
	if len(out) == 0 {
		return entries, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	for name, data := range raw {
		var candidates []npmOutdatedEntry
		if len(data) > 0 && data[0] == '[' {
			if err := json.Unmarshal(data, &candidates); err != nil {
				return nil, err
			}
		} else {
			var candidate npmOutdatedEntry
			if err := json.Unmarshal(data, &candidate); err != nil {
				return nil, err
			}
			candidates = []npmOutdatedEntry{candidate}
		}
		entries[name] = candidates
	}
	return entries, nil
}

type npmInstallGroup struct {
	Workspace string
	Packages  []string
}

func npmLatestInstallGroups(entries map[string][]npmOutdatedEntry, only []string, workspaces map[string]string) []npmInstallGroup {
	grouped := map[string]map[string]bool{}
	for name, candidates := range entries {
		if !inOnly(name, only) {
			continue
		}
		for _, candidate := range candidates {
			if candidate.Latest == "" || candidate.Latest == candidate.Current {
				continue
			}
			workspace := workspaces[candidate.Dependent]
			if grouped[workspace] == nil {
				grouped[workspace] = map[string]bool{}
			}
			grouped[workspace][name+"@latest"] = true
		}
	}
	groups := make([]npmInstallGroup, 0, len(grouped))
	for workspace, packageSet := range grouped {
		packages := make([]string, 0, len(packageSet))
		for pkg := range packageSet {
			packages = append(packages, pkg)
		}
		sort.Strings(packages)
		groups = append(groups, npmInstallGroup{Workspace: workspace, Packages: packages})
	}
	// Upgrade workspaces before the root so peer-coupled packages resolve
	// against one consistent version when the root install updates the lock.
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Workspace == "" {
			return false
		}
		if groups[j].Workspace == "" {
			return true
		}
		return groups[i].Workspace < groups[j].Workspace
	})
	return groups
}

func npmOutdatedChange(entries []npmOutdatedEntry, includeMajor bool, workspaces map[string]string) (npmOutdatedEntry, string, bool) {
	for _, preferRoot := range []bool{true, false} {
		for _, entry := range entries {
			_, isWorkspace := workspaces[entry.Dependent]
			if preferRoot == isWorkspace {
				continue
			}
			to := entry.Wanted
			if includeMajor {
				to = entry.Latest
			}
			if to != "" && to != entry.Current {
				return entry, to, true
			}
		}
	}
	return npmOutdatedEntry{}, "", false
}

func npmWorkspaceNames(ctx context.Context, dir string) (map[string]string, error) {
	out, err := runCmd(ctx, dir, "npm", "query", ".workspace", "--json")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var entries []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if len(out) > 0 {
		if err := json.Unmarshal(out, &entries); err != nil {
			return nil, err
		}
	}
	workspaces := make(map[string]string, len(entries)*2)
	for _, entry := range entries {
		if entry.Name != "" {
			workspaces[entry.Name] = entry.Name
			if alias := path.Base(entry.Location); alias != "." && alias != "/" {
				workspaces[alias] = entry.Name
			}
		}
	}
	return workspaces, nil
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
