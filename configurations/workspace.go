package configurations

import (
	"path"
	"path/filepath"

	"github.com/codefly-dev/core/shared"
)

const GlobalConfigurationName = "codefly.yaml"

// Workspace configuration for codefly CLI
type Workspace struct {
	Organization string `yaml:"organization"`
	Domain       string `yaml:"domain"`

	// Projects in the global configuration
	Projects       []*ProjectReference `yaml:"projects"`
	CurrentProject string              `yaml:"current-project,omitempty"`

	// Internal
	FullDir string `yaml:"-"`
}

// A GlobalConfigurationInputer abstracts away global configuration and default of project creation
type GlobalConfigurationInputer interface {
	// Fetch instantiates the input
	Fetch() error
	// Organization is now global
	Organization() string
	// Domain associated with the organization
	Domain() string
	// CreateDefaultProject returns true if a default project should be created
	CreateDefaultProject() bool
	// ProjectBuilder abstracts away the configuration of Project creation
	ProjectBuilder() ProjectBuilder
}

// InitGlobal initializes the global configuration of codefly
// GlobalConfigurationInputer: setup the configuration and defaults
// Override: policy to replace existing configuration
func InitGlobal(getter GlobalConfigurationInputer, override Override) {
	logger := shared.NewLogger("configurations.InitCodefly")
	logger.Tracef("creating if needed global configuration dir: %v", globalConfigDir)

	dir := SolveDirOrCreate(globalConfigDir)

	// Check if already exists
	if ExistsAtDir[Workspace](dir) && !override.Override(Path[Workspace](dir)) {
		logger.Debugf("global configuration already exists and no override")
		return
	}
	logger.Debugf("to <%s>", dir)

	err := getter.Fetch()
	if err != nil {
		shared.UnexpectedExitOnError(err, "cannot fetch global configuration")
	}
	global := Workspace{
		FullDir:      dir,
		Organization: getter.Organization(),
		Domain:       getter.Domain(),
	}
	err = SaveToDir[Workspace](&global, dir)
	shared.ExitOnError(err, "cannot save global configuration")
	if getter.CreateDefaultProject() {
		logger.Debugf("creating default project")
		_, err := NewProject("fix me")
		shared.UnexpectedExitOnError(err, "cannot create default project")
	}
}

// Dir returns the absolute path to the global configuration directory
func (g *Workspace) Dir() string {
	return g.FullDir
}

func (g *Workspace) Relative(dir string) string {
	rel, err := filepath.Rel(g.Dir(), dir)
	shared.ExitOnError(err, "cannot compute relative path from workspace")
	return rel
}

// Current returns the current global configuration
func Current() (*Workspace, error) {
	logger := shared.NewLogger("configurations.Current")
	if global != nil {
		return global, nil
	}
	g, err := LoadFromDir[Workspace](GlobalConfigurationDir())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load global configuration")
	}
	g.FullDir = GlobalConfigurationDir()
	global = g
	return global, nil
}

func SaveCurrent() {
	err := SaveToDir[Workspace](global, global.Dir())
	shared.UnexpectedExitOnError(err, "cannot save global configuration")
}

func Reset() {
	global = nil
	currentProject = nil
}

func GlobalConfigurationDir() string {
	return globalConfigDir
}

func GlobalProjectRoot() string {
	return globalProjectRoot
}

// MustCurrent returns the current global configuration
func MustCurrent() *Workspace {
	if global == nil {
		g, err := Current()
		shared.ExitOnError(err, "cannot load current global configuration")
		global = g
	}
	return global
}

// This is where the global configuration is stored
// default to ~/.codefly/.
var globalConfigDir string

// This is where we create projects from:
// default to ~/codefly

var (
	globalProjectRoot string
	global            *Workspace
)

func init() {
	globalConfigDir = path.Join(HomeDir(), ".codefly")
	globalProjectRoot = path.Join(HomeDir(), "codefly")
}

func LoadGlobalConfiguration() {
	logger := shared.NewLogger("configurations.LoadGlobalConfiguration")
	p := Path[Workspace](globalConfigDir)
	logger.Debugf("from <%s>", p)
}

func OverrideWorkspaceConfigDir(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceConfigDir")
	logger.Debugf("overriding global workspace configuration directory to <%s>", dir)
	globalConfigDir = dir
}

func OverrideWorkspaceProjectRoot(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceProjectRoot")
	logger.Debugf("overriding global project root to <%s>", dir)
	globalProjectRoot = dir
}
