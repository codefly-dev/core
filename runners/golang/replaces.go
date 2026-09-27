package golang

import (
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// localReplaceModuleDirs returns the directory of every module the go.mod in
// modDir replaces with a local path, transitively (a replaced module's own
// local replaces too), in the order first found. Those directories are part of
// what `go build` compiles, so a binary cache keyed only on modDir would serve
// a stale binary after a change in any of them.
//
// A go.mod that cannot be read or parsed contributes nothing here: the build
// itself reports it.
func localReplaceModuleDirs(modDir string) []string {
	seen := map[string]bool{}
	if abs, err := filepath.Abs(modDir); err == nil {
		seen[abs] = true
	}
	var out []string
	var walk func(dir string)
	walk = func(dir string) {
		gomod := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(gomod)
		if err != nil {
			return
		}
		file, err := modfile.Parse(gomod, data, nil)
		if err != nil {
			return
		}
		for _, replace := range file.Replace {
			if replace.New.Version != "" || !modfile.IsDirectoryPath(replace.New.Path) {
				continue
			}
			target := replace.New.Path
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			target, err := filepath.Abs(target)
			if err != nil || seen[target] {
				continue
			}
			seen[target] = true
			out = append(out, target)
			walk(target)
		}
	}
	walk(modDir)
	return out
}
