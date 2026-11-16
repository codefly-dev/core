// Package discovery is how codefly looks for a file in a directory and its
// ancestors — a codefly.local.yaml overlay, the go.mod or Cargo.toml that
// governs a source directory, a workspace or module configuration — without
// leaving the tree that directory belongs to.
//
// An upward search that runs to the filesystem root reads whatever happens to
// sit above the tree: the overlay another checkout left in a shared parent, a
// go.mod some tool wrote into the system temporary directory. Two boundaries
// stop it:
//
//   - Ceilings, always. The search never enters a ceiling directory or any
//     directory above one, starting below it. The system temporary directory
//     is always a ceiling: it belongs to no workspace, and every test's
//     temporary directory lives under it. CODEFLY_CEILING_DIRECTORIES adds
//     more, separated like PATH, as GIT_CEILING_DIRECTORIES does for git.
//   - The repository root, when the caller asks for it (Repository). The
//     search reads the directory holding .git and stops there: a file above
//     the repository is never that repository's.
//
// The starting directory itself is always read.
package discovery

import (
	"os"
	"path/filepath"
	"strings"
)

// CeilingEnvironment lists directories, separated by os.PathListSeparator,
// that a search never enters from below.
const CeilingEnvironment = "CODEFLY_CEILING_DIRECTORIES"

// Bound selects where a search stops besides the ceilings.
type Bound int

const (
	// Ceilings stops only at the ceilings.
	Ceilings Bound = iota
	// Repository also stops at the repository root: the nearest directory
	// holding .git (a directory, or a file in a git worktree) is the last
	// one read.
	Repository
)

// Ancestors is start followed by each of its parents, nearest first, up to
// the bound: the directories a search for a file from start may read.
func Ancestors(start string, bound Bound) []string {
	current, err := filepath.Abs(start)
	if err != nil {
		current = filepath.Clean(start)
	}
	ceilings := ceilingsAbove(current)
	directories := []string{current}
	for {
		if bound == Repository && holdsRepository(current) {
			return directories
		}
		parent := filepath.Dir(current)
		if parent == current || ceilings[parent] || ceilings[resolved(parent)] {
			return directories
		}
		current = parent
		directories = append(directories, current)
	}
}

// Find is the first of Ancestors(start, bound) holding name, and whether
// there is one.
func Find(start, name string, bound Bound) (string, bool) {
	for _, directory := range Ancestors(start, bound) {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return directory, true
		}
	}
	return "", false
}

func holdsRepository(directory string) bool {
	_, err := os.Stat(filepath.Join(directory, ".git"))
	return err == nil
}

// ceilingsAbove is every ceiling start lies strictly below, together with
// each directory above it: the search stops before entering any of them.
// A ceiling start is not under does not bound it.
func ceilingsAbove(start string) map[string]bool {
	candidates := []string{os.TempDir()}
	for _, listed := range filepath.SplitList(os.Getenv(CeilingEnvironment)) {
		if listed = strings.TrimSpace(listed); listed != "" {
			candidates = append(candidates, listed)
		}
	}
	forms := []string{start, resolved(start)}
	out := map[string]bool{}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		for _, ceiling := range []string{filepath.Clean(absolute), resolved(absolute)} {
			for _, form := range forms {
				if !strictlyUnder(form, ceiling) {
					continue
				}
				for directory := ceiling; ; directory = filepath.Dir(directory) {
					out[directory] = true
					if filepath.Dir(directory) == directory {
						break
					}
				}
			}
		}
	}
	return out
}

func strictlyUnder(path, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// resolved is path with its symbolic links evaluated, or path itself when it
// cannot be: macOS's temporary directory sits behind /var → /private/var.
func resolved(path string) string {
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		return evaluated
	}
	return filepath.Clean(path)
}
