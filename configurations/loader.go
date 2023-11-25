package configurations

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/configurations/generation"
	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

type Configuration interface{}

func SolveDir(dir string) string {
	logger := shared.NewLogger("configurations.SolveDir")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			shared.ExitOnError(err, "cannot get current directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("Solving path <%s> from current directory", dir)
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		shared.ExitOnError(err, "cannot find directory")
	}
	return dir
}

func SolveDirOrCreate(dir string) string {
	logger := shared.NewLogger("configurations.SolveDirOrCreate")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			shared.ExitOnError(err, "cannot get current directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("solving path <%s> from current directory", dir)
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			shared.ExitOnError(err, "cannot create directory")
		}
	}
	return dir
}

func Path[C Configuration](dir string) string {
	if err := shared.CheckDirectory(dir); err != nil {
		if filepath.IsLocal(dir) {
			cur, err := os.Getwd()
			if err != nil {
				shared.ExitOnError(err, "cannot get current directory")
			}
			dir = filepath.Join(cur, dir)
		}
	}
	var c C
	switch any(c).(type) {
	case Workspace:
		return path.Join(dir, GlobalConfigurationName)
	case Project:
		return path.Join(dir, ProjectConfigurationName)
	case Application:
		return path.Join(dir, ApplicationConfigurationName)
	case Service:
		return path.Join(dir, ServiceConfigurationName)
	case generation.Service:
		return path.Join(dir, generation.ServiceGenerationConfigurationName)
	case Agent:
		return path.Join(dir, AgentConfigurationName)
	// case Library:
	//	return path.Join(dir, LibraryConfigurationName)
	// case LibraryGeneration:
	//	return path.Join(dir, LibraryGenerationConfigurationName)
	default:
		panic(fmt.Errorf("unknown configuration type <%T>", c))
	}
}

func ExistsAtDir[C Configuration](dir string) bool {
	var p string
	var c C
	switch any(c).(type) {
	case Workspace:
		p = path.Join(dir, GlobalConfigurationName)
	case Project:
		p = path.Join(dir, ProjectConfigurationName)
	case Application:
		p = path.Join(dir, ApplicationConfigurationName)
	case Service:
		p = path.Join(dir, ServiceConfigurationName)
	default:
		panic(fmt.Errorf("unknown configuration type <%T>", c))
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return false
	}
	return true
}

func TypeName[C Configuration]() string {
	var c C
	return fmt.Sprintf("%T", c)
}

func LoadFromDir[C Configuration](dir string) (*C, error) {
	p := Path[C](dir)
	return LoadFromPath[C](p)
}

func LoadFromPath[C Configuration](p string) (*C, error) {
	logger := shared.NewLogger("configurations.LoadFromPath[%s]<%s>", TypeName[C](), p)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, logger.Errorf("path doesn't exist")
	}
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read path %s: %s", p, err)
	}
	return LoadFromBytes[C](content)
}

func LoadFromBytes[C Configuration](content []byte) (*C, error) {
	var config C
	err := yaml.Unmarshal(content, &config)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal service configuration: %s", err)
	}
	return &config, nil
}

type OverrideHandler interface {
	Replace(p string) bool
}

type SaveOption struct {
	OverrideHandler
}

type SaveOptionFunc func(*SaveOption)

func WithOverride(handler OverrideHandler) SaveOptionFunc {
	return func(o *SaveOption) {
		o.OverrideHandler = handler
	}
}

func SkipOverride() SaveOptionFunc {
	return func(o *SaveOption) {
		o.OverrideHandler = &SkipOverrideHandler{}
	}
}

type AskOverrideHandler struct{}

var overridingPath map[string]bool

func init() {
	overridingPath = make(map[string]bool)
}

func (a AskOverrideHandler) Replace(p string) bool {
	if overridingPath[p] {
		return true
	}
	ok := shared.Confirm(fmt.Sprintf("File %s already exists, want to override it?", p))
	overridingPath[p] = ok
	return ok
}

var _ OverrideHandler = (*AskOverrideHandler)(nil)

func AskOverride() SaveOptionFunc {
	return func(o *SaveOption) {
		o.OverrideHandler = &AskOverrideHandler{}
	}
}

type SkipOverrideHandler struct{}

func (h *SkipOverrideHandler) Replace(f string) bool {
	return false
}

func SaveToDir[C Configuration](c *C, dir string, opts ...SaveOptionFunc) error {
	logger := shared.NewLogger("configurations.SaveToDir[%s]", TypeName[C]())
	if !shared.DirectoryExists(dir) {
		return logger.Errorf("directory doesn't exist")
	}
	option := SaveOptions(opts)
	file := Path[C](dir)
	if shared.FileExists(file) {
		if !option.OverrideHandler.Replace(file) {
			return nil
		}
	}
	content, err := yaml.Marshal(*c)
	if err != nil {
		return logger.Wrapf(err, "cannot marshal")
	}
	err = os.WriteFile(file, content, 0o644)
	if err != nil {
		return logger.Wrapf(err, "cannot write")
	}
	return nil
}

func SaveOptions(opts []SaveOptionFunc) SaveOption {
	saveOption := SaveOption{
		OverrideHandler: &AskOverrideHandler{},
	}
	for _, opt := range opts {
		opt(&saveOption)
	}
	return saveOption
}

// FindUp looks for a service configuration in the current directory and up
func FindUp[C Configuration](cur string) (*C, error) {
	logger := shared.NewLogger("configurations.FindUp[%s]", TypeName[C]())
	logger.Tracef("from <%s>", cur)
	for {
		// Look for a service configuration
		p := Path[C](cur)
		if _, err := os.Stat(p); err == nil {
			return LoadFromDir[C](cur)
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			return nil, logger.Errorf("cannot find %s configuration: reached root directory", TypeName[C]())
		}
	}
}
