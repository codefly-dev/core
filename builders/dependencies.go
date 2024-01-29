package builders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/shared"
)

type Dependency struct {
	pathSelect shared.PathSelect
	components []string
	dir        string
}

func NewDependency(components ...string) *Dependency {
	return &Dependency{
		components: components,
	}
}

func (dep *Dependency) WithPathSelect(sel shared.PathSelect) *Dependency {
	dep.pathSelect = sel
	return dep
}

func (dep *Dependency) Hash(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("builders.Dependency.Hash")
	h := sha256.New()
	for _, path := range dep.components {
		if !filepath.IsAbs(path) && dep.dir != "" {
			path = filepath.Join(dep.dir, path)
		}
		if shared.FileExists(path) {
			err := addFileHash(ctx, h, path)
			if err != nil {
				return "", err
			}
			continue
		}
		if !shared.DirectoryExists(path) {
			return "", fmt.Errorf("path %s does not exist", path)
		}
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Skip directories
				return nil
			}
			// PathSelect hash themselves
			if strings.HasSuffix(p, ".hash") {
				return nil
			}
			if dep.pathSelect != nil && !dep.pathSelect.Keep(p) {
				return nil
			}
			return addFileHash(ctx, h, p)
		})
		if err != nil {
			return "", w.Wrapf(err, "cannot walk path %s", path)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (dep *Dependency) Localize(dir string) *Dependency {
	dep.dir = dir
	return dep
}

func (dep *Dependency) Components() []string {
	return dep.components
}

func (dep *Dependency) Keep(path string) bool {
	if dep.pathSelect == nil {
		return true
	}
	return dep.pathSelect.Keep(path)
}

type Dependencies struct {
	Name       string
	Components []*Dependency

	dir string
}

func NewDependencies(name string, components ...*Dependency) *Dependencies {
	return &Dependencies{
		Name:       name,
		Components: components,
	}
}

func (dep *Dependencies) hashFile() string {
	return filepath.Join(dep.dir, fmt.Sprintf(".%s.hash", strings.ToLower(dep.Name)))
}

func (dep *Dependencies) Localize(dir string) *Dependencies {
	dep.dir = dir
	for _, c := range dep.Components {
		c.Localize(dir)
	}
	return dep
}

// AddDependency adds a dependency to the list of dependencies
func (dep *Dependencies) AddDependencies(dependencies ...*Dependency) *Dependencies {
	for _, dependency := range dependencies {
		dependency.Localize(dep.dir)
		dep.Components = append(dep.Components, dependency)
	}
	return dep
}

func (dep *Dependencies) Updated(ctx context.Context) (bool, error) {
	hash, err := dep.Hash(ctx)
	if err != nil {
		return true, err
	}
	current := dep.LoadHash(ctx)
	if current == hash {
		return false, nil
	}
	err = dep.WriteHash(ctx, hash)
	if err != nil {
		return true, err
	}

	return true, nil
}

func (dep *Dependencies) WriteHash(ctx context.Context, hash string) error {
	w := wool.Get(ctx).In("builders.Dependency.WriteHash")
	if dep.dir == "" {
		return nil
	}
	// New or overwrite
	f, err := os.Create(dep.hashFile())
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	_, err = fmt.Fprintf(f, "%s", hash)
	if err != nil {
		return w.Wrapf(err, "cannot write hash")
	}
	return nil
}

func (dep *Dependencies) LoadHash(ctx context.Context) string {
	w := wool.Get(ctx).In("builders.Dependencies.LoadHash")
	if dep.dir == "" {
		return configurations.Unknown
	}
	f, err := os.Open(dep.hashFile())
	if err != nil {
		return ""
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	var hash string
	_, err = fmt.Fscanf(f, "%s", &hash)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(hash)
}

func (dep *Dependencies) Hash(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("builders.Dependencies.Hash")
	h := sha256.New()
	for _, component := range dep.Components {
		hash, err := component.Hash(ctx)
		if err != nil {
			return "", w.Wrapf(err, "cannot get hash for component %s", component.components)
		}
		_, err = io.WriteString(h, hash)
		if err != nil {
			return "", w.Wrapf(err, "cannot write hash for component %s", component.components)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (dep *Dependencies) Clone() *Dependencies {
	return &Dependencies{
		Name:       dep.Name,
		Components: dep.Components,
	}
}

func addFileHash(ctx context.Context, h io.Writer, path string) error {
	w := wool.Get(ctx).In("builders.addFileHash")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	return nil
}
