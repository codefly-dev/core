package configurations

import (
	"github.com/hygge-io/hygge/pkg/core"
	"path"
	"path/filepath"
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

type ProjectReference struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path"`
}

type GlobalGetter interface {
	Fetch() error
	Organization() string
	Domain() string
	CreateDefaultProject() bool
	ProjectGetter() ProjectBuilder
}

// InitGlobal creates a new global configuration in the global configuration directory
func InitGlobal(getter GlobalGetter, override Override) {
	logger := core.NewLogger("configurations.InitCodefly")
	logger.Debugf("initializing codefly")

	dir := SolveDirOrCreate(globalConfigDir)

	// Check if already exists
	if ExistsAtDir[Workspace](dir) && !override.Override(Path[Workspace](dir)) {
		logger.Debugf("global configuration already exists and no override")
		return
	}
	logger.Debugf("to <%s>", dir)

	getter.Fetch()
	global := Workspace{
		FullDir:      dir,
		Organization: getter.Organization(),
		Domain:       getter.Domain(),
	}
	err := SaveToDir[Workspace](&global, dir)
	core.ExitOnError(err, "cannot save global configuration")
	if getter.CreateDefaultProject() {
		logger.Debugf("creating default project")
		err := NewProject(getter.ProjectGetter())
		core.UnexpectedExitOnError(err, "cannot create default project")
	}
}

// Dir returns the absolute path to the global configuration directory
func (g *Workspace) Dir() string {
	return g.FullDir
}

func (g *Workspace) Relative(dir string) string {
	rel, err := filepath.Rel(g.Dir(), dir)
	core.ExitOnError(err, "cannot compute relative path from workspace")
	return rel
}

// Current returns the current global configuration
func Current() (*Workspace, error) {
	logger := core.NewLogger("configurations.Current")
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
	core.UnexpectedExitOnError(err, "cannot save global configuration")
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
		core.ExitOnError(err, "cannot load current global configuration")
		global = g
	}
	return global
}

// This is where the global configuration is stored
// default to ~/.codefly/.
var globalConfigDir string

// This is where we create projects from:
// default to ~/codefly

var globalProjectRoot string
var global *Workspace

func init() {
	globalConfigDir = path.Join(HomeDir(), ".codefly")
	globalProjectRoot = path.Join(HomeDir(), "codefly")
}

func LoadGlobalConfiguration() {
	logger := core.NewLogger("configurations.LoadGlobalConfiguration")
	p := Path[Workspace](globalConfigDir)
	logger.Debugf("from <%s>", p)
}

func OverrideWorkspaceConfigDir(dir string) {
	logger := core.NewLogger("configurations.OverrideWorkspaceConfigDir")
	logger.Debugf("overriding global  to <%s>", dir)
	globalConfigDir = dir
}

func OverrideWorkspaceProjectRoot(dir string) {
	logger := core.NewLogger("configurations.OverrideWorkspaceProjectRoot")
	logger.Debugf("overriding global project root to <%s>", dir)
	globalProjectRoot = dir
}
