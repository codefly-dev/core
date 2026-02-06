package resources

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/wool"
)

const LibraryConfigurationName = "library.codefly.yaml"

// Library represents internal shared code that can be used by services
type Library struct {
	Kind        string             `yaml:"kind"`
	Name        string             `yaml:"name"`
	Description string             `yaml:"description,omitempty"`
	Version     string             `yaml:"version"`
	Languages   []*LanguageExport  `yaml:"languages,omitempty"`
	Git         *GitConfig         `yaml:"git,omitempty"`
	LibraryDeps []*LibraryReference `yaml:"library-dependencies,omitempty"`

	// Internal
	dir string
}

// LanguageExport defines how a library is exported for a specific language
type LanguageExport struct {
	Name    string   `yaml:"name"`    // "go", "python", "typescript"
	Agent   string   `yaml:"agent"`   // Library agent identifier
	Path    string   `yaml:"path"`    // Relative path to language code
	Exports []string `yaml:"exports"` // Package/module names exported
}

// GitConfig for libraries that are git repositories
type GitConfig struct {
	Remote string `yaml:"remote,omitempty"` // Git remote URL
	Branch string `yaml:"branch,omitempty"` // Default branch
	Commit string `yaml:"commit,omitempty"` // Pinned commit
	Tag    string `yaml:"tag,omitempty"`    // Version tag
}

// LibraryReference is used for dependencies between libraries
type LibraryReference struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"` // Semver constraint
}

// LibraryDependency is used when a service depends on a library
type LibraryDependency struct {
	Name      string   `yaml:"name"`
	Version   string   `yaml:"version"`   // Semver constraint
	Languages []string `yaml:"languages"` // Which language exports needed
}

// NewLibrary creates a new Library
func NewLibrary(ctx context.Context, name string) (*Library, error) {
	w := wool.Get(ctx).In("NewLibrary", wool.NameField(name))

	lib := &Library{
		Kind:    "library",
		Name:    name,
		Version: "0.0.1",
	}

	// Validate name
	if name == "" {
		return nil, w.NewError("library name cannot be empty")
	}

	return lib, nil
}

// Dir returns the library directory
func (lib *Library) Dir() string {
	return lib.dir
}

// WithDir sets the library directory
func (lib *Library) WithDir(dir string) {
	lib.dir = dir
}

// Save saves the library configuration
func (lib *Library) Save(ctx context.Context) error {
	return lib.SaveToDir(ctx, lib.dir)
}

// SaveToDir saves the library to a specific directory
func (lib *Library) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("Library.SaveToDir", wool.NameField(lib.Name))

	if dir == "" {
		return w.NewError("library directory is empty")
	}

	return SaveToDir[Library](ctx, lib, dir)
}

// Proto converts to a map representation (proto will be generated later)
func (lib *Library) Proto(_ context.Context) map[string]any {
	proto := map[string]any{
		"name":        lib.Name,
		"description": lib.Description,
		"version":     lib.Version,
	}

	if len(lib.Languages) > 0 {
		var languages []map[string]any
		for _, lang := range lib.Languages {
			languages = append(languages, map[string]any{
				"name":    lang.Name,
				"agent":   lang.Agent,
				"path":    lang.Path,
				"exports": lang.Exports,
			})
		}
		proto["languages"] = languages
	}

	if lib.Git != nil {
		proto["git"] = map[string]any{
			"remote": lib.Git.Remote,
			"branch": lib.Git.Branch,
			"commit": lib.Git.Commit,
			"tag":    lib.Git.Tag,
		}
	}

	return proto
}

// LoadLibraryFromDir loads a library from a directory
func LoadLibraryFromDir(ctx context.Context, dir string) (*Library, error) {
	w := wool.Get(ctx).In("LoadLibraryFromDir", wool.DirField(dir))

	lib, err := LoadFromDir[Library](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	lib.dir = dir
	return lib, nil
}

// AddLanguage adds a language export to the library
func (lib *Library) AddLanguage(ctx context.Context, name, agent, relPath string) error {
	w := wool.Get(ctx).In("Library.AddLanguage", wool.Field("language", name))

	// Check if language already exists
	for _, lang := range lib.Languages {
		if lang.Name == name {
			return w.NewError("language %s already exists", name)
		}
	}

	lib.Languages = append(lib.Languages, &LanguageExport{
		Name:  name,
		Agent: agent,
		Path:  relPath,
	})

	return nil
}

// GetLanguage returns a language export by name
func (lib *Library) GetLanguage(name string) *LanguageExport {
	for _, lang := range lib.Languages {
		if lang.Name == name {
			return lang
		}
	}
	return nil
}

// LanguagePath returns the absolute path to a language's code
func (lib *Library) LanguagePath(lang *LanguageExport) string {
	return filepath.Join(lib.dir, lang.Path)
}

// IsGitSubmodule checks if the library is a git submodule
func (lib *Library) IsGitSubmodule(ctx context.Context) bool {
	gitDir := filepath.Join(lib.dir, ".git")
	exists, _ := shared.FileExists(ctx, gitDir)
	return exists
}

// GetGitVersion returns the current git tag/version if available
func (lib *Library) GetGitVersion(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("Library.GetGitVersion")

	if !lib.IsGitSubmodule(ctx) {
		return lib.Version, nil
	}

	// Get the current tag
	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--exact-match")
	cmd.Dir = lib.dir
	out, err := cmd.Output()
	if err != nil {
		// No tag, try commit
		cmd = exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
		cmd.Dir = lib.dir
		out, err = cmd.Output()
		if err != nil {
			return "", w.Wrapf(err, "failed to get git version")
		}
		return strings.TrimSpace(string(out)), nil
	}

	tag := strings.TrimSpace(string(out))
	// Remove 'v' prefix if present
	tag = strings.TrimPrefix(tag, "v")
	return tag, nil
}

// CreateGitTag creates a git tag for the current version
func (lib *Library) CreateGitTag(ctx context.Context) error {
	w := wool.Get(ctx).In("Library.CreateGitTag")

	if !lib.IsGitSubmodule(ctx) {
		return w.NewError("library is not a git repository")
	}

	tag := "v" + lib.Version
	cmd := exec.CommandContext(ctx, "git", "tag", "-a", tag, "-m", fmt.Sprintf("Version %s", lib.Version))
	cmd.Dir = lib.dir
	if err := cmd.Run(); err != nil {
		return w.Wrapf(err, "failed to create git tag %s", tag)
	}

	return nil
}

// Unique returns a unique identifier for the library
func (lib *Library) Unique() string {
	return lib.Name
}

// Identity returns the library identity
func (lib *Library) Identity() *LibraryIdentity {
	return &LibraryIdentity{
		Name:    lib.Name,
		Version: lib.Version,
		Path:    lib.dir,
	}
}

// LibraryIdentity uniquely identifies a library
type LibraryIdentity struct {
	Name      string
	Workspace string
	Version   string
	Path      string
}

// Workspace library management

// LoadLibraryFromName loads a library by name from a workspace
func (workspace *Workspace) LoadLibraryFromName(ctx context.Context, name string) (*Library, error) {
	w := wool.Get(ctx).In("Workspace.LoadLibraryFromName", wool.NameField(name))

	libDir := path.Join(workspace.Dir(), "libraries", name)
	exists, err := shared.DirectoryExists(ctx, libDir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !exists {
		return nil, w.NewError("library %s not found", name)
	}

	return LoadLibraryFromDir(ctx, libDir)
}

// LoadLibraries loads all libraries in the workspace
func (workspace *Workspace) LoadLibraries(ctx context.Context) ([]*Library, error) {
	w := wool.Get(ctx).In("Workspace.LoadLibraries")

	libsDir := path.Join(workspace.Dir(), "libraries")
	exists, err := shared.DirectoryExists(ctx, libsDir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !exists {
		return []*Library{}, nil
	}

	entries, err := os.ReadDir(libsDir)
	if err != nil {
		return nil, w.Wrapf(err, "failed to read libraries directory")
	}

	var libs []*Library
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		libDir := path.Join(libsDir, entry.Name())
		lib, err := LoadLibraryFromDir(ctx, libDir)
		if err != nil {
			w.Warn("failed to load library", wool.Field("name", entry.Name()), wool.ErrField(err))
			continue
		}
		libs = append(libs, lib)
	}

	return libs, nil
}

// CreateLibrary creates a new library in the workspace
func (workspace *Workspace) CreateLibrary(ctx context.Context, name string, languages []string) (*Library, error) {
	w := wool.Get(ctx).In("Workspace.CreateLibrary", wool.NameField(name))

	libDir := path.Join(workspace.Dir(), "libraries", name)

	exists, err := shared.DirectoryExists(ctx, libDir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if exists {
		return nil, w.NewError("library %s already exists", name)
	}

	// Create directory
	if _, err := shared.CheckDirectoryOrCreate(ctx, libDir); err != nil {
		return nil, w.Wrapf(err, "failed to create library directory")
	}

	lib, err := NewLibrary(ctx, name)
	if err != nil {
		return nil, w.Wrap(err)
	}
	lib.dir = libDir

	// Add languages
	for _, lang := range languages {
		langPath := lang + "/"
		if err := lib.AddLanguage(ctx, lang, "", langPath); err != nil {
			return nil, w.Wrap(err)
		}

		// Create language directory
		langDir := filepath.Join(libDir, langPath)
		if _, err := shared.CheckDirectoryOrCreate(ctx, langDir); err != nil {
			return nil, w.Wrapf(err, "failed to create language directory")
		}
	}

	// Save configuration
	if err := lib.Save(ctx); err != nil {
		return nil, w.Wrap(err)
	}

	return lib, nil
}

// AddLibraryAsSubmodule adds an external library as a git submodule
func (workspace *Workspace) AddLibraryAsSubmodule(ctx context.Context, name, remote, branch string) (*Library, error) {
	w := wool.Get(ctx).In("Workspace.AddLibraryAsSubmodule", wool.NameField(name))

	libDir := path.Join(workspace.Dir(), "libraries", name)

	// Ensure libraries directory exists
	libsDir := path.Join(workspace.Dir(), "libraries")
	if _, err := shared.CheckDirectoryOrCreate(ctx, libsDir); err != nil {
		return nil, w.Wrap(err)
	}

	// Add as git submodule
	args := []string{"submodule", "add"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	args = append(args, remote, libDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspace.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, w.Wrapf(err, "failed to add submodule: %s", string(out))
	}

	// Load the library
	lib, err := LoadLibraryFromDir(ctx, libDir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	return lib, nil
}

// SyncLibrarySubmodules syncs all library submodules
func (workspace *Workspace) SyncLibrarySubmodules(ctx context.Context) error {
	w := wool.Get(ctx).In("Workspace.SyncLibrarySubmodules")

	cmd := exec.CommandContext(ctx, "git", "submodule", "update", "--init", "--recursive")
	cmd.Dir = workspace.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "failed to sync submodules: %s", string(out))
	}

	return nil
}
