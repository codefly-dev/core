package configurations

import (
	"context"
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
	logger := shared.NewLogger().With("configurations.SolveDir")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			shared.ExitOnError(err, "cannot get active directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("Solving path <%s> from active directory", dir)
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		shared.ExitOnError(err, "cannot find directory")
	}
	return dir
}

func SolveDirOrCreate(dir string) string {
	logger := shared.NewLogger().With("configurations.SolveDirOrCreate")
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			shared.ExitOnError(err, "cannot get active directory")
		}
		dir = filepath.Join(cur, dir)
		logger.Tracef("solving path <%s> from active directory", dir)
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

func ConfigurationFile[C Configuration]() string {
	var c C
	switch any(c).(type) {
	case Info:
		return InfoConfigurationName
	case Workspace:
		return WorkspaceConfigurationName
	case Project:
		return ProjectConfigurationName
	case Application:
		return ApplicationConfigurationName
	case Service:
		return ServiceConfigurationName
	case generation.Service:
		return generation.ServiceGenerationConfigurationName
	case Agent:
		return AgentConfigurationName
	default:
		panic(fmt.Errorf("unknown configuration type <%T>", c))
	}
}

func Path[C Configuration](dir string) string {
	if err := shared.CheckDirectory(dir); err != nil {
		if filepath.IsLocal(dir) {
			cur, err := os.Getwd()
			if err != nil {
				shared.ExitOnError(err, "cannot get active directory")
			}
			dir = filepath.Join(cur, dir)
		}
	}
	return path.Join(dir, ConfigurationFile[C]())
}

func ExistsAtDir[C Configuration](dir string) bool {
	var p string
	var c C
	switch any(c).(type) {
	case Workspace:
		p = path.Join(dir, WorkspaceConfigurationName)
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

func LoadFromFs[C any](fs shared.FileSystem) (*C, error) {
	logger := shared.NewLogger().With("configurations.LoadFromFs[%s]", TypeName[C]())
	content, err := fs.ReadFile(shared.NewFile(ConfigurationFile[C]()))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot read file")
	}
	conf, err := LoadFromBytes[C](content)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load from bytes")
	}
	return conf, nil
}

func LoadFromDir[C Configuration](ctx context.Context, dir string) (*C, error) {
	p := Path[C](dir)
	return LoadFromPath[C](ctx, p)
}

func LoadFromPath[C Configuration](ctx context.Context, p string) (*C, error) {
	logger := shared.GetBaseLogger(ctx).With("configurations.LoadFromPath[%s]<%s>", TypeName[C](), p)
	logger.SetLogMethod(shared.AllActions).Tracef("loading")
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

func SaveToDir[C Configuration](ctx context.Context, c *C, dir string) error {
	logger := shared.GetBaseLogger(ctx).With("SaveToDir[%s]", TypeName[C]())
	logger.Debugf("saving to <%s>", dir)
	err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return logger.Wrapf(err, "cannot check directory")
	}
	file := Path[C](dir)
	if shared.FileExists(file) {
		override := shared.GetOverride(ctx)
		if !override.Replace(file) {
			logger.Debugf("file <%s> already exists and override is not set", file)
			return nil
		}
	}
	content, err := yaml.Marshal(*c)
	if err != nil {
		return logger.Wrapf(err, "cannot marshal")
	}
	err = os.WriteFile(file, content, 0600)
	if err != nil {
		return logger.Wrapf(err, "cannot write")
	}
	return nil
}

// FindUp looks for a service configuration in the active directory and up
func FindUp[C Configuration](ctx context.Context) (*C, error) {
	logger := shared.GetBaseLogger(ctx).With("configurations.FindUp[%s]", TypeName[C]())
	cur, err := os.Getwd()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get active directory")
	}
	logger.Tracef("from <%s>", cur)
	for {
		// Look for a service configuration
		p := Path[C](cur)
		if _, err := os.Stat(p); err == nil {
			return LoadFromDir[C](ctx, cur)
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			return nil, logger.Errorf("cannot find %s configuration: reached root directory", TypeName[C]())
		}
	}
}
