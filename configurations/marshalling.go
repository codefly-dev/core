package configurations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"
	"gopkg.in/yaml.v3"
)

func TypeName[C Configuration]() string {
	var c C
	return fmt.Sprintf("%T", c)
}

func LoadFromFs[C Configuration](fs shared.FileSystem) (*C, error) {
	w := wool.Get(context.Background()).In("configurations.LoadFromFs", wool.Field("type", TypeName[C]()))
	content, err := fs.ReadFile(ConfigurationFile[C]())
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
	var atRoot bool
	for {
		// Look for a configuration
		p, err := Path[C](ctx, cur)
		if err != nil {
			return nil, w.Wrapf(err, "cannot get path")
		}
		if _, err := os.Stat(p); err == nil {
			w.Trace("found", wool.DirField(p))
			return &cur, nil
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			if atRoot {
				return nil, nil
			}
			atRoot = true
		}
	}
}
