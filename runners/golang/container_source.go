package golang

// container_source.go answers one question for the container backend: which
// host directories must the container carry for the service's source to exist
// inside it.
//
// The container mounts the composing workspace at its own path, and every
// process is then given the service's source directory as its working
// directory. That is enough only while the source lives inside the workspace
// tree. A composed service does not: a playground names its modules with
// `path: ../modules/...`, an overlay points at a sibling checkout, and a
// resolved module lives in the module cache. The container then started with a
// working directory that did not exist inside it ("chdir to cwd ... no such
// file or directory", reported as `go mod download` exiting 127).
//
// What the source needs is derived from the source itself, never widened to a
// common ancestor: its directory, the module that owns it, and the directories
// its go.mod and go.work name on disk (`replace => ../lib`, `use ../sibling`).
// Each is mounted read-only at its own path. They belong to whoever owns that
// checkout or cache entry, not to the workspace composing them.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// containerSourceDir is the directory a container process works in for a
// source the workspace declares at sourceDir. A source that resolves outside
// the workspace is named by its real path: that is the path its relative
// `replace` and `use` directives are resolved against on the host, so it is
// the only path at which the container's `..` means the same directory.
func containerSourceDir(workspaceDir, sourceDir string) string {
	resolved, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return sourceDir
	}
	workspaceRoot := workspaceDir
	if root, err := filepath.EvalSymlinks(workspaceDir); err == nil {
		workspaceRoot = root
	}
	if withinDir(workspaceRoot, resolved) {
		return sourceDir
	}
	return resolved
}

// sourceDirectories lists every directory the Go toolchain reads to build the
// source: the source directory, its module root, the modules a source-owned
// go.work uses, and every local replacement those declare.
func sourceDirectories(sourceDir, moduleDir string, workspace *sourceGoWorkspace) ([]string, error) {
	dirs := []string{sourceDir}
	modules := []string{}
	if moduleDir != "" {
		dirs = append(dirs, moduleDir)
		modules = append(modules, moduleDir)
	}
	if workspace != nil {
		dirs = append(dirs, workspace.root)
		dirs = append(dirs, workspace.moduleDirs...)
		dirs = append(dirs, workspace.externalDirs...)
		modules = append(modules, workspace.moduleDirs...)
	}
	for _, module := range modules {
		replaced, err := localReplacements(module)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, replaced...)
	}
	return dirs, nil
}

// localReplacements are the directories a module's go.mod replaces modules
// with. Only the main module's replacements apply to a build, so this is not
// followed into the replacements' own go.mod files.
func localReplacements(moduleDir string) ([]string, error) {
	modPath := filepath.Join(moduleDir, "go.mod")
	data, err := os.ReadFile(modPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", modPath, err)
	}
	mod, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", modPath, err)
	}
	var dirs []string
	for _, replace := range mod.Replace {
		if dir, ok := replacementDir(moduleDir, replace); ok {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// replacementDir is the directory a replace directive points at; false for a
// replacement by another module version, which the module cache serves, and
// for a directory that is not there: the toolchain reports that one itself
// when the build needs it, where a bind mount of a missing path would refuse
// to start the container for a replacement nothing imports.
func replacementDir(base string, replace *modfile.Replace) (string, bool) {
	if replace == nil || replace.New.Version != "" || replace.New.Path == "" {
		return "", false
	}
	dir := replace.New.Path
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(base, dir)
	}
	dir = filepath.Clean(dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// sourceMounts reduces the directories a source needs to the ones a container
// has to be given: those the workspace mount does not already carry, without
// one nested in another.
//
// It refuses a directory that would hand the container the user's home
// directory or the filesystem root. A go.mod sitting in $HOME, or a `replace`
// pointing there, is a mistake in that checkout; mounting it would put every
// credential on the machine inside a build container to make it work.
func sourceMounts(workspaceDir string, dirs []string) ([]string, error) {
	workspaceDir = filepath.Clean(workspaceDir)
	var outside []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("source directory %q is not absolute: a container cannot be given a path relative to this process", dir)
		}
		if withinDir(workspaceDir, dir) {
			continue
		}
		if err := refuseBroadMount(dir); err != nil {
			return nil, err
		}
		outside = append(outside, dir)
	}
	sort.Strings(outside)
	var mounts []string
	for _, dir := range outside {
		covered := false
		for _, kept := range mounts {
			if withinDir(kept, dir) {
				covered = true
				break
			}
		}
		if !covered {
			mounts = append(mounts, dir)
		}
	}
	return mounts, nil
}

func refuseBroadMount(dir string) error {
	if dir == string(filepath.Separator) {
		return fmt.Errorf("refusing to mount the filesystem root into a container as service source")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	homes := []string{filepath.Clean(home)}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		homes = append(homes, resolved)
	}
	candidates := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		for _, h := range homes {
			if withinDir(candidate, h) {
				return fmt.Errorf("refusing to mount %s into a container as service source: it contains the home directory %s", dir, h)
			}
		}
	}
	return nil
}

// withinDir reports whether dir is root or lies beneath it, lexically.
func withinDir(root, dir string) bool {
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
