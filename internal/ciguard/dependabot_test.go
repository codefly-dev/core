package ciguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type dependabotConfig struct {
	Version int `yaml:"version"`
	Updates []struct {
		Ecosystem   string   `yaml:"package-ecosystem"`
		Directory   string   `yaml:"directory"`
		Directories []string `yaml:"directories"`
		Limit       int      `yaml:"open-pull-requests-limit"`
		Groups      map[string]struct {
			Patterns    []string `yaml:"patterns"`
			UpdateTypes []string `yaml:"update-types"`
			AppliesTo   string   `yaml:"applies-to"`
		} `yaml:"groups"`
	} `yaml:"updates"`
}

func loadDependabot(t *testing.T) (dependabotConfig, string) {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".github", "dependabot.yml"))
	require.NoError(t, err)
	var cfg dependabotConfig
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, 2, cfg.Version)
	require.NotEmpty(t, cfg.Updates)
	return cfg, root
}

// dirsFor returns every directory the config covers for one ecosystem.
func dirsFor(cfg dependabotConfig, ecosystem string) map[string]bool {
	out := map[string]bool{}
	for _, u := range cfg.Updates {
		if u.Ecosystem != ecosystem {
			continue
		}
		if u.Directory != "" {
			out[u.Directory] = true
		}
		for _, d := range u.Directories {
			out[d] = true
		}
	}
	return out
}

// isFixture reports whether a manifest is test data or installed/vendored
// third-party content rather than something this repository ships.
//
// These are deliberately NOT covered: bumping a fixture breaks the tests that
// assert against its pinned contents, and vendor/node_modules trees belong to
// a manifest that is covered in its own right.
func isFixture(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		switch seg {
		case "testdata", "vendor", "node_modules", ".git":
			return true
		}
	}
	return false
}

// Every manifest this repository actually ships must be covered by a
// dependabot entry, or its dependencies are silently never updated.
//
// This caught companions/proto/facades/go -- a real published module whose
// TypeScript sibling was covered while it was not.
func TestDependabotCoversEveryShippedManifest(t *testing.T) {
	cfg, root := loadDependabot(t)

	// manifest filename -> dependabot ecosystem.
	ecosystemOf := map[string]string{
		"go.mod":         "gomod",
		"package.json":   "npm",
		"pyproject.toml": "pip",
		"Dockerfile":     "docker",
	}

	found := map[string]map[string]bool{} // ecosystem -> set of dirs
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if isFixture(rel) {
				return fs.SkipDir
			}
			return nil
		}
		eco, ok := ecosystemOf[d.Name()]
		if !ok || isFixture(rel) {
			return nil
		}
		dir := "/" + filepath.ToSlash(filepath.Dir(rel))
		if dir == "/." {
			dir = "/"
		}
		if found[eco] == nil {
			found[eco] = map[string]bool{}
		}
		found[eco][dir] = true
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, found, "walked the tree and found no manifests at all")

	for eco, dirs := range found {
		covered := dirsFor(cfg, eco)
		require.NotEmpty(t, covered, "no dependabot entry for ecosystem %q", eco)
		for dir := range dirs {
			require.True(t, covered[dir],
				"%s manifest in %q is shipped but no dependabot entry covers it, "+
					"so its dependencies are never updated. Add it to the %q entry's "+
					"`directories`, or exclude it deliberately if it is a fixture.",
				eco, dir, eco)
		}
	}
}

// Every configured directory must actually exist. A typo makes Dependabot
// report an error on its own page and open nothing -- a silent no-op that no
// other check would notice.
func TestDependabotDirectoriesExist(t *testing.T) {
	cfg, root := loadDependabot(t)
	for _, u := range cfg.Updates {
		dirs := append([]string{}, u.Directories...)
		if u.Directory != "" {
			dirs = append(dirs, u.Directory)
		}
		require.NotEmpty(t, dirs, "entry for %q configures no directory", u.Ecosystem)
		for _, d := range dirs {
			require.DirExists(t, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(d, "/"))),
				"%s entry points at %q, which does not exist", u.Ecosystem, d)
		}
	}
}

// Each ecosystem must be ONE catch-all group that carries NO `update-types:`
// filter, so a week yields at most one pull request per ecosystem.
//
// The missing filter is load-bearing, not an omission. `update-types:` matches
// only semver update types, so a Docker base image pinned by digest matches
// none of them, falls OUT of the group, and Dependabot opens a separate
// ungrouped pull request for it -- one per directory. Omitting the filter is
// what makes `patterns: "*"` mean everything, digests included.
//
// Majors therefore share the group with minor and patch. That is a deliberate
// trade: quarantining majors in a second group is a guaranteed extra pull
// request every time one lands, and the alternative it buys (a stuck major
// never holding routine updates hostage) is bounded here because Dependabot
// rebases an existing grouped pull request rather than needing a fresh slot
// for later bumps. Do not re-split majors without also accepting that cost.
//
// Security updates are exempt: they are not capped by open-pull-requests-limit
// and are declared with `applies-to: security-updates`, so they are ignored
// here and may be grouped however they like.
func TestEveryEcosystemIsOneUnfilteredCatchAllGroup(t *testing.T) {
	cfg, _ := loadDependabot(t)

	for _, u := range cfg.Updates {
		require.NotEmpty(t, u.Groups, "%s: no groups configured", u.Ecosystem)

		versionGroups := 0
		for name, g := range u.Groups {
			if g.AppliesTo == "security-updates" {
				continue
			}
			versionGroups++

			require.Empty(t, g.UpdateTypes,
				"%s group %q sets update-types %v. That filter only matches semver "+
					"update types, so a digest-pinned dependency matches none of them, "+
					"drops out of the group, and opens its own ungrouped pull request "+
					"per directory. Drop the filter so `patterns: \"*\"` covers "+
					"everything.", u.Ecosystem, name, g.UpdateTypes)

			require.Equal(t, []string{"*"}, g.Patterns,
				"%s group %q must be the catch-all `*` so nothing falls outside it "+
					"into its own pull request", u.Ecosystem, name)
		}

		require.Equal(t, 1, versionGroups,
			"%s: expected exactly one catch-all version-update group; %d of them "+
				"means at least %d pull requests per ecosystem per week",
			u.Ecosystem, versionGroups, versionGroups)
		require.GreaterOrEqual(t, u.Limit, versionGroups,
			"%s: open-pull-requests-limit %d cannot hold its %d version-update "+
				"group(s), so Dependabot silently opens nothing",
			u.Ecosystem, u.Limit, versionGroups)
	}
}
