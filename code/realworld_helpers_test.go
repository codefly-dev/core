package code

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepo defines a public Go repository used for real-world analysis testing.
type TestRepo struct {
	Name           string
	CloneURL       string
	Module         string
	MinPackages    int
	MinSymbols     int
	KnownFunctions []string
	KnownTypes     []string
	MultiPackage   bool
	SearchPattern  string
	SearchMinHits  int
}

// AllTestRepos returns the 8 target repos ordered simple to complex.
func AllTestRepos() []TestRepo {
	return []TestRepo{
		{
			Name:           "fatih_color",
			CloneURL:       "https://github.com/fatih/color",
			Module:         "github.com/fatih/color",
			MinSymbols:     10,
			KnownFunctions: []string{"New"},
			KnownTypes:     []string{"Color"},
			SearchPattern:  "Sprintf",
			SearchMinHits:  1,
		},
		{
			Name:           "mitchellh_mapstructure",
			CloneURL:       "https://github.com/mitchellh/mapstructure",
			Module:         "github.com/mitchellh/mapstructure",
			MinSymbols:     5,
			KnownFunctions: []string{"Decode"},
			KnownTypes:     []string{"DecoderConfig"},
			SearchPattern:  "Decode",
			SearchMinHits:  2,
		},
		{
			Name:           "tidwall_gjson",
			CloneURL:       "https://github.com/tidwall/gjson",
			Module:         "github.com/tidwall/gjson",
			MinSymbols:     15,
			KnownFunctions: []string{"Get", "Parse"},
			KnownTypes:     []string{"Result"},
			SearchPattern:  "func.*Get",
			SearchMinHits:  1,
		},
		{
			Name:           "spf13_pflag",
			CloneURL:       "https://github.com/spf13/pflag",
			Module:         "github.com/spf13/pflag",
			MinSymbols:     20,
			KnownFunctions: []string{"Parse"},
			KnownTypes:     []string{"FlagSet"},
			SearchPattern:  "String",
			SearchMinHits:  1,
		},
		{
			Name:           "rs_zerolog",
			CloneURL:       "https://github.com/rs/zerolog",
			Module:         "github.com/rs/zerolog",
			MinPackages:    2,
			MinSymbols:     15,
			KnownFunctions: []string{"New"},
			KnownTypes:     []string{"Logger", "Event"},
			MultiPackage:   true,
			SearchPattern:  "Logger",
			SearchMinHits:  3,
		},
		{
			Name:           "go_chi_chi",
			CloneURL:       "https://github.com/go-chi/chi",
			Module:         "github.com/go-chi/chi/v5",
			MinPackages:    2,
			MinSymbols:     10,
			KnownFunctions: []string{"NewRouter"},
			KnownTypes:     []string{"Mux"},
			MultiPackage:   true,
			SearchPattern:  "Handler",
			SearchMinHits:  2,
		},
		{
			Name:           "gorilla_mux",
			CloneURL:       "https://github.com/gorilla/mux",
			Module:         "github.com/gorilla/mux",
			MinSymbols:     10,
			KnownFunctions: []string{"NewRouter"},
			KnownTypes:     []string{"Router", "Route"},
			SearchPattern:  "Route",
			SearchMinHits:  3,
		},
		{
			Name:           "charmbracelet_lipgloss",
			CloneURL:       "https://github.com/charmbracelet/lipgloss",
			Module:         "github.com/charmbracelet/lipgloss",
			MinPackages:    2,
			MinSymbols:     10,
			KnownFunctions: []string{"NewStyle"},
			KnownTypes:     []string{"Style"},
			MultiPackage:   true,
			SearchPattern:  "Render",
			SearchMinHits:  1,
		},
	}
}

// EnsureRepo returns the path to a test repo managed as a git submodule under
// testdata/repos/<name>. Skips the test if the submodule hasn't been initialized
// (run `git submodule update --init` from the core root).
func EnsureRepo(t *testing.T, repo TestRepo) string {
	t.Helper()
	dir := filepath.Join("testdata", "repos", repo.Name)

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Skipf("submodule %s not initialized (run: git submodule update --init --recursive)", repo.Name)
	}
	return dir
}

// LoadGoFiles reads all .go source files (excluding tests and vendor) from a
// directory into a map[string]string suitable for conventions.Detect().
func LoadGoFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if isGoTestFile(path) {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		files[rel] = string(data)
		return nil
	})
	return files
}

func isGoTestFile(path string) bool {
	base := filepath.Base(path)
	return len(base) > 8 && base[len(base)-8:] == "_test.go"
}
