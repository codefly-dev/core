package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations/generation"
	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

type Configuration interface{}

func SolveDir(dir string) (string, error) {
	w := wool.Get(context.Background()).In("configurations.SolveDir", wool.Field("dir", dir))
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			return "", w.Wrapf(err, "cannot get active directory")
		}
		dir = filepath.Join(cur, dir)
		w.Trace("solved path")
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "", w.Wrapf(err, "directory doesn't exist")
	}
	return dir, nil
}

func SolveDirOrCreate(dir string) (string, error) {
	w := wool.Get(context.Background()).In("configurations.SolveDirOrCreate", wool.Field("dir", dir))
	if filepath.IsLocal(dir) || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "..") {
		cur, err := os.Getwd()
		if err != nil {
			return "", w.Wrapf(err, "cannot get active directory")
		}
		dir = filepath.Join(cur, dir)
		w.Trace("solved path")
	}
	// Validate
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			return "", w.Wrapf(err, "cannot create directory")
		}
	}
	return dir, nil
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

func Path[C Configuration](ctx context.Context, dir string) (string, error) {
	w := wool.Get(ctx).In("configurations.Path", wool.DirField(dir), wool.GenericField[C]())
	if _, err := shared.CheckDirectory(ctx, dir); err != nil {
		if filepath.IsLocal(dir) {
			cur, err := os.Getwd()
			if err != nil {
				return "", w.Wrap(err)
			}
			dir = filepath.Join(cur, dir)
		}
	}
	return path.Join(dir, ConfigurationFile[C]()), nil
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
	w := wool.Get(context.Background()).In("configurations.LoadFromFs", wool.Field("type", TypeName[C]()))
	content, err := fs.ReadFile(shared.NewFile(ConfigurationFile[C]()))
	if err != nil {
		return nil, w.Wrapf(err, "cannot read file")
	}
	conf, err := LoadFromBytes[C](content)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load from bytes")
	}
	return conf, nil
}

func LoadFromDir[C Configuration](ctx context.Context, dir string) (*C, error) {
	w := wool.Get(ctx).In("configurations.LoadFromDir")
	p, err := Path[C](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return LoadFromPath[C](ctx, p)
}

func LoadFromPath[C Configuration](ctx context.Context, p string) (*C, error) {
	w := wool.Get(ctx).In("configurations.LoadWorkspace", wool.Field("path", p))
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, w.NewError("path doesn't exist <%s>", p)
	}
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, w.NewError("cannot read path %s: %s", p, err)
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
	w := wool.Get(ctx).In("SaveToDir[%s]", wool.GenericField[C](), wool.DirField(dir))
	w.Trace("saving")
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return w.Wrapf(err, "cannot check directory")
	}
	file, err := Path[C](ctx, dir)
	if err != nil {
		return w.Wrapf(err, "cannot get path")
	}
	if shared.FileExists(file) {
		override := shared.GetOverride(ctx)
		if !override.Replace(file) {
			w.Debug("file already exists without override: skipping", wool.FileField(file))
			return nil
		}
	}
	content, err := yaml.Marshal(*c)
	if err != nil {
		return w.Wrapf(err, "cannot marshal")
	}
	err = os.WriteFile(file, content, 0600)
	if err != nil {
		return w.Wrapf(err, "cannot write")
	}
	return nil
}

// FindUp looks for a configuration in the active directory and up
func FindUp[C Configuration](ctx context.Context) (*string, error) {
	w := wool.Get(ctx).In("configurations.FindUp", wool.GenericField[C]())
	cur, err := os.Getwd()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get active directory")
	}
	for {
		// Look for a service configuration
		p, err := Path[C](ctx, cur)
		if err != nil {
			return nil, w.Wrapf(err, "cannot get path")
		}
		if _, err := os.Stat(p); err == nil {
			return &cur, nil
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			return nil, nil
		}
	}
}
