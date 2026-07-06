package python

// Dependency-file knowledge for the PYTHON ecosystem — which project files
// declare/pin dependencies, and which to read first when healing an
// environment block. This is PLUGIN knowledge: the Mind brain calls
// DependencyConfigCandidates with a file listing and never hardcodes Python
// file names itself (it used to, which meant Go/TS repos got no evidence and
// the heal edit-gate silently degraded).

import (
	"path"
	"sort"
	"strings"
)

// DependencyConfigCandidates filters a repo file listing down to the python
// dependency/config files a heal should read as evidence, best-first.
func DependencyConfigCandidates(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		clean := strings.Trim(path.Clean(strings.ReplaceAll(p, "\\", "/")), "/")
		if clean == "" || clean == "." || seen[clean] || !IsDependencyConfigPath(clean) {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := dependencyFileRank(out[i]), dependencyFileRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// IsDependencyConfigPath reports whether p declares/pins python dependencies.
func IsDependencyConfigPath(p string) bool {
	base := strings.ToLower(path.Base(p))
	dir := strings.ToLower(path.Dir(p))
	if dir == "." {
		switch {
		case base == "requirements.txt",
			base == "requirements.in",
			base == "constraints.txt",
			base == "pyproject.toml",
			base == "setup.cfg",
			base == "setup.py",
			base == "tox.ini",
			base == "pipfile",
			strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"),
			strings.HasPrefix(base, "constraints") && strings.HasSuffix(base, ".txt"):
			return true
		}
	}
	if dir == "requirements" && strings.HasSuffix(base, ".txt") {
		return true
	}
	return false
}

func dependencyFileRank(p string) int {
	base := strings.ToLower(path.Base(p))
	dir := strings.ToLower(path.Dir(p))
	switch base {
	case "requirements.txt":
		return 0
	case "pyproject.toml":
		return 1
	case "setup.cfg":
		return 2
	case "setup.py":
		return 3
	case "tox.ini":
		return 4
	case "constraints.txt":
		return 5
	}
	if dir == "." && strings.HasPrefix(base, "constraints") && strings.HasSuffix(base, ".txt") {
		return 5
	}
	if strings.Contains(strings.ToLower(p), "requirements") {
		return 6
	}
	return 7
}
