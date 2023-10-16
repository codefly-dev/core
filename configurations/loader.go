package configurations

import (
	"fmt"
	"github.com/hygge-io/hygge/pkg/core"
	"gopkg.in/yaml.v3"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Configuration interface {
}

func SolveDir(dir string) string {
	logger := core.NewLogger("configurations.SolveDir")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			core.ExitOnError(err, "cannot get current directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("Solving path <%s> from current directory", dir)
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		core.ExitOnError(err, "cannot find directory")
	}
	return dir
}

func SolveDirOrCreate(dir string) string {
	logger := core.NewLogger("configurations.SolveDirOrCreate")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			core.ExitOnError(err, "cannot get current directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("solving path <%s> from current directory", dir)
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			core.ExitOnError(err, "cannot create directory")
		}
	}
	return dir
}

func Path[C Configuration](dir string) string {
	if err := core.CheckDirectory(dir); err != nil {
		if filepath.IsLocal(dir) {
			cur, err := os.Getwd()
			if err != nil {
				core.ExitOnError(err, "cannot get current directory")
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
	case Library:
		return path.Join(dir, LibraryConfigurationName)
	case LibraryGeneration:
		return path.Join(dir, LibraryGenerationConfigurationName)
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
	case Library:
		p = path.Join(dir, LibraryConfigurationName)
	case LibraryGeneration:
		p = path.Join(dir, LibraryGenerationConfigurationName)
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
	var config C
	logger := core.NewLogger("configurations.LoadFromPath[%T]<%s>", config, p)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, logger.Errorf("path for %v does not exist", TypeName[C]())
	}
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read path %s: %s", p, err)
	}
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		return nil, logger.Errorf("cannot unmarshal service configuration: %s", err)
	}
	return &config, nil
}

func SaveToDir[C Configuration](c *C, dir string) error {
	logger := core.NewLogger("configurations.SaveToDir[%s]", TypeName[C]())
	if f, err := os.Stat(dir); os.IsNotExist(err) || !f.IsDir() {
		return logger.Wrapf(err, "cannot find NewDir: %s", dir)
	}
	file := Path[C](dir)
	content, err := yaml.Marshal(*c)
	if err != nil {
		return logger.Wrapf(err, "cannot marshal")
	}
	err = os.WriteFile(file, content, 0644)
	if err != nil {
		return logger.Wrapf(err, "cannot write")
	}
	return nil
}

func FindUp[C Configuration](cur string) (*C, error) {
	logger := core.NewLogger("configurations.FindUp[%s]", TypeName[C]())
	logger.Debugf("Solving <%s>", cur)
	for {
		// Look for a service configuration
		p := Path[C](cur)
		logger.Debugf("looking for %s", p)
		if _, err := os.Stat(p); err == nil {
			return LoadFromDir[C](cur)
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			return nil, logger.Errorf("cannot find service configuration: reached root directory")
		}
	}
}
