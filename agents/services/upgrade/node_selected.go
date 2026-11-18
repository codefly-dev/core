package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// npmSaveExact is the Save of a group whose packages are declared at an exact
// version, and stay so.
const npmSaveExact = "exact"

// npmManifest is what one package.json of the project declares: the root's
// (Workspace empty) or a workspace's.
type npmManifest struct {
	Workspace string
	Declared  map[string]string
}

// splitNpmOnly separates the packages of Only that name a version
// (`pkg@1.2.3`, `@scope/pkg@1.2.3`) from the ones that do not.
func splitNpmOnly(only []string) (names []string, selected map[string]string) {
	selected = map[string]string{}
	for _, pkg := range only {
		if at := strings.LastIndex(pkg, "@"); at > 0 {
			selected[pkg[:at]] = pkg[at+1:]
			continue
		}
		names = append(names, pkg)
	}
	return names, selected
}

// npmSelectedInstalls is the `npm install` invocations that move each selected
// package to the version it names, and the changes they make.
//
// `npm update` resolves inside the declared range, so a dependency declared at
// an exact version never moves under it; naming the version is how it does. A
// package moves in every manifest that declares it and keeps the way that
// manifest declares it: exact stays exact, a caret or tilde range keeps its
// operator. A package no manifest declares is refused: this moves a
// dependency, it does not add one.
func npmSelectedInstalls(selected map[string]string, manifests []npmManifest) ([]npmInstallGroup, []*builderv0.UpgradeChange, error) {
	type key struct{ workspace, save string }
	grouped := map[key][]string{}
	changed := map[string]*builderv0.UpgradeChange{}
	for name, version := range selected {
		declared := false
		for _, manifest := range manifests {
			spec, ok := manifest.Declared[name]
			if !ok {
				continue
			}
			declared = true
			save, from := npmSaveExact, spec
			if strings.HasPrefix(spec, "^") || strings.HasPrefix(spec, "~") {
				save, from = spec[:1], spec[1:]
			}
			if from == version {
				continue
			}
			k := key{manifest.Workspace, save}
			grouped[k] = append(grouped[k], name+"@"+version)
			if _, seen := changed[name]; !seen || manifest.Workspace == "" {
				changed[name] = &builderv0.UpgradeChange{Package: name, From: from, To: version}
			}
		}
		if !declared {
			return nil, nil, fmt.Errorf("%s is not a dependency this project declares: `%s@%s` moves a declared dependency to a version, it does not add one", name, name, version)
		}
	}
	groups := make([]npmInstallGroup, 0, len(grouped))
	for k, packages := range grouped {
		sort.Strings(packages)
		groups = append(groups, npmInstallGroup{Workspace: k.workspace, Packages: packages, Save: k.save})
	}
	// Workspaces before the root, as npmLatestInstallGroups orders them.
	sort.Slice(groups, func(i, j int) bool {
		if (groups[i].Workspace == "") != (groups[j].Workspace == "") {
			return groups[j].Workspace == ""
		}
		if groups[i].Workspace != groups[j].Workspace {
			return groups[i].Workspace < groups[j].Workspace
		}
		return groups[i].Save < groups[j].Save
	})
	changes := make([]*builderv0.UpgradeChange, 0, len(changed))
	for _, change := range changed {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Package < changes[j].Package })
	return groups, changes, nil
}

// installArgs is the `npm` arguments that install the group.
func (g npmInstallGroup) installArgs() []string {
	args := append([]string{"install"}, g.Packages...)
	switch g.Save {
	case "":
	case npmSaveExact:
		args = append(args, "--save-exact")
	default:
		args = append(args, "--save-prefix="+g.Save)
	}
	if g.Workspace != "" {
		args = append(args, "--workspace", g.Workspace)
	}
	return args
}

// readNpmManifests reads the root package.json of dir and the one of each of
// its workspaces.
func readNpmManifests(ctx context.Context, dir string) ([]npmManifest, error) {
	root, err := readNpmManifest(filepath.Join(dir, "package.json"), "")
	if err != nil {
		return nil, err
	}
	manifests := []npmManifest{root}
	out, err := runCmd(ctx, dir, "npm", "query", ".workspace", "--json")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var workspaces []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if len(out) > 0 {
		if err := json.Unmarshal(out, &workspaces); err != nil {
			return nil, err
		}
	}
	for _, workspace := range workspaces {
		if workspace.Name == "" || workspace.Location == "" {
			continue
		}
		manifest, err := readNpmManifest(filepath.Join(dir, filepath.FromSlash(workspace.Location), "package.json"), workspace.Name)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func readNpmManifest(file, workspace string) (npmManifest, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return npmManifest{}, err
	}
	var raw struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return npmManifest{}, fmt.Errorf("%s: %w", file, err)
	}
	manifest := npmManifest{Workspace: workspace, Declared: map[string]string{}}
	for _, section := range []map[string]string{raw.OptionalDependencies, raw.DevDependencies, raw.Dependencies} {
		for name, spec := range section {
			manifest.Declared[name] = spec
		}
	}
	return manifest, nil
}
