// Package companionindex is where a process learns which version of each
// companion image to run. Core declares how a companion is built (its recipe,
// companions/<name>/info.codefly.yaml) but compiles no version in: which
// images a run uses is data its caller supplies, so a companion release moves
// no core release and no agent re-pin.
//
// A process gets its index one of three ways, first match wins:
//
//   - Register, called by the program that owns the choice (the CLI, at
//     startup, with the index it carries or the one a workspace names);
//   - the CODEFLY_COMPANION_INDEX environment variable, which the agent manager
//     sets for every agent it spawns from the registered index, so an agent
//     runs the companions its caller chose;
//   - in a test binary only, the recipes of the core source the test was
//     compiled against (see recipesOfThisSource).
//
// Anything else is an error that says so: there is no default version.
package companionindex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Masterminds/semver"
	"gopkg.in/yaml.v3"
)

// Environment carries an index to a child process, as name=version pairs
// joined by commas: "go=0.0.20,proto=0.0.30".
const Environment = "CODEFLY_COMPANION_INDEX"

// Index maps a companion name (its directory under companions/) to the image
// version to run.
type Index map[string]string

var (
	mu         sync.RWMutex
	registered Index
)

// Register makes index the process's companion index. A nil index clears it.
func Register(index Index) error {
	index = clone(index)
	if err := index.validate(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	registered = index
	return nil
}

// Registered is the index Register set, nil when none was.
func Registered() Index {
	mu.RLock()
	defer mu.RUnlock()
	return clone(registered)
}

// Current is the index this process runs with.
func Current() (Index, error) {
	if index := Registered(); index != nil {
		return index, nil
	}
	if raw, ok := os.LookupEnv(Environment); ok {
		index, err := ParseEnviron(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", Environment, err)
		}
		return index, nil
	}
	if testing.Testing() {
		return recipesOfThisSource()
	}
	return nil, fmt.Errorf("no companion index: the caller supplies companion versions (companionindex.Register, or %s for a spawned process)", Environment)
}

// Version is the version of the named companion in the current index.
func Version(name string) (string, error) {
	index, err := Current()
	if err != nil {
		return "", err
	}
	return index.Version(name)
}

// Version is the named companion's version.
func (index Index) Version(name string) (string, error) {
	version, ok := index[name]
	if !ok {
		return "", fmt.Errorf("the companion index names no %q companion (it names %s)", name, strings.Join(index.names(), ", "))
	}
	return version, nil
}

// Parse reads an index document: a YAML mapping of companion name to version.
func Parse(data []byte) (Index, error) {
	var index Index
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("cannot read companion index: %w", err)
	}
	return index, index.validate()
}

// Environ is the index as Environment carries it.
func (index Index) Environ() string {
	pairs := make([]string, 0, len(index))
	for _, name := range index.names() {
		pairs = append(pairs, name+"="+index[name])
	}
	return strings.Join(pairs, ",")
}

// ParseEnviron reads the Environment form.
func ParseEnviron(raw string) (Index, error) {
	index := Index{}
	for _, pair := range strings.Split(raw, ",") {
		name, version, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			return nil, fmt.Errorf("companion index entry %q is not name=version", pair)
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("companion index names %q twice", name)
		}
		index[name] = version
	}
	return index, index.validate()
}

// FromRecipes reads the version every companion recipe under
// <root>/companions/ declares: the index of a core checkout, for the tools
// that build and publish companions from one.
func FromRecipes(root string) (Index, error) {
	paths, err := filepath.Glob(filepath.Join(root, "companions", "*", "info.codefly.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no companion recipe under %s", filepath.Join(root, "companions"))
	}
	index := Index{}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var recipe struct {
			Version string `yaml:"version"`
		}
		if err := yaml.Unmarshal(content, &recipe); err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
		index[filepath.Base(filepath.Dir(path))] = recipe.Version
	}
	return index, index.validate()
}

// recipesOfThisSource is the index a test binary falls back to: the recipes of
// the core source it was compiled from, which is the same set a test of that
// source exercises. It is never consulted outside a test binary.
func recipesOfThisSource() (Index, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("no companion index, and this test binary cannot locate its core source")
	}
	return FromRecipes(filepath.Join(filepath.Dir(file), "..", ".."))
}

// validate refuses an index that names no valid companion or version, and
// writes each version the way its image is tagged.
func (index Index) validate() error {
	for name, version := range index {
		if name == "" || strings.ContainsAny(name, "=, \t\n") {
			return fmt.Errorf("companion index names an invalid companion %q", name)
		}
		parsed, err := semver.NewVersion(version)
		if err != nil {
			return fmt.Errorf("companion %q has version %q, which is not a semantic version: %w", name, version, err)
		}
		// The canonical spelling, as the image tag is written.
		index[name] = parsed.String()
	}
	return nil
}

func (index Index) names() []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func clone(index Index) Index {
	if index == nil {
		return nil
	}
	out := make(Index, len(index))
	for name, version := range index {
		out[name] = version
	}
	return out
}

