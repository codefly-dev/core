package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/codefly-dev/core/version"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/generation"
	"github.com/codefly-dev/core/shared"
)

type Configuration interface{}

func ConfigurationFile[C Configuration]() string {
	var c C
	switch any(c).(type) {
	case version.Info:
		return version.InfoConfigurationName
	case Workspace:
		return WorkspaceConfigurationName
	case Module:
		return ModuleConfigurationName
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
	w := wool.Get(ctx).In("configurations.Dir", wool.DirField(dir), wool.GenericField[C]())
	if _, err := shared.DirectoryExists(ctx, dir); err != nil {
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
	case Module:
		p = path.Join(dir, ModuleConfigurationName)
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
