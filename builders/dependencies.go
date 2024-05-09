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
		exists, err := shared.FileExists(ctx, path)
		if err != nil {
			return "", w.Wrapf(err, "cannot check if file exists %s", path)
		}
		if exists {
			err := addFileHash(ctx, h, path)
			if err != nil {
				return "", err
			}
			continue
		}
		exists, err = shared.DirectoryExists(ctx, path)
		if err != nil {
			return "", w.Wrapf(err, "cannot check if directory exists %s", path)
		}
		if !exists {
			continue
		}
		err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
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

	root    string
	current string

	cache string
}

// MakeDependencySummary outputs a summary of the dependency
func MakeDependencySummary(dep *Dependency) string {
	return fmt.Sprintf("[%s]", strings.Join(dep.Components(), ","))
}

// MakeDependenciesSummary outputs a summary of the dependencies
func MakeDependenciesSummary(deps *Dependencies) string {
	var summary []string
	for _, dep := range deps.Components {
		summary = append(summary, MakeDependencySummary(dep))
	}
	return strings.Join(summary, ",")
}

func NewDependencies(name string, components ...*Dependency) *Dependencies {
	return &Dependencies{
		Name:       name,
		Components: components,
	}
}

func (dep *Dependencies) String() string {
	return MakeDependenciesSummary(dep)
}

func (dep *Dependencies) hashFile() string {
	return filepath.Join(dep.cache, fmt.Sprintf("%s.hash", strings.ToLower(dep.Name)))
}

func (dep *Dependencies) Localize(root string) {
	dep.root = root
	for _, c := range dep.Components {
		c.Localize(root)
	}
}

// AddDependencies adds dependencies
func (dep *Dependencies) AddDependencies(dependencies ...*Dependency) *Dependencies {
	for _, dependency := range dependencies {
		dependency.Localize(dep.root)
		dep.Components = append(dep.Components, dependency)
	}
	return dep
}

type AcceptChange func(ctx context.Context) error

func (dep *Dependencies) WithCache(location string) *Dependencies {
	dep.cache = location
	return dep
}

func (dep *Dependencies) Updated(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("builders.ServiceDependencies.Updated")
	hash, err := dep.Hash(ctx)
	if err != nil {
		return true, err
	}
	w.Debug("calculate hash", wool.Field("hash", hash))
	current := dep.LoadHash(ctx)
	w.Debug("current hash", wool.Field("hash", current))
	if current == hash {
		return false, nil
	}
	dep.current = hash
	return true, nil
}

func (dep *Dependencies) UpdateCache(ctx context.Context) error {
	err := dep.WriteHash(ctx, dep.current)
	if err != nil {
		return err
	}
	return nil
}

func (dep *Dependencies) WriteHash(ctx context.Context, hash string) error {
	w := wool.Get(ctx).In("builders.Dependency.WriteHash")
	if dep.cache == "" {
		w.Warn("no cache location: in directory")
		dep.cache = dep.root
	}
	w.Debug("write hash", wool.FileField(dep.hashFile()))
	// New or overwrite
	f, err := os.Create(dep.hashFile())
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		err = f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	_, err = fmt.Fprintf(f, "%s", hash)
	if err != nil {
		return w.Wrapf(err, "cannot write hash")
	}
	w.Debug("wrote hash to", wool.FileField(f.Name()))
	return nil
}

func (dep *Dependencies) LoadHash(ctx context.Context) string {
	w := wool.Get(ctx).In("builders.ServiceDependencies.LoadHash")
	if dep.cache == "" {
		w.Warn("no cache location: in directory")
		dep.cache = dep.root
	}
	f, err := os.Open(dep.hashFile())
	if err != nil {
		return ""
	}
	defer func(f *os.File) {
		err = f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	var hash string
	_, err = fmt.Fscanf(f, "%s", &hash)
	if err != nil {
		w.Error("cannot read hash", wool.Field("error", err))
		return ""
	}
	w.Debug("read hash from", wool.FileField(f.Name()), wool.Field("hash", hash))
	return strings.TrimSpace(hash)
}

func (dep *Dependencies) Hash(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("builders.ServiceDependencies.Hash")
	h := sha256.New()
	for _, component := range dep.Components {
		w.Debug("hashing component", wool.Field("component", component.components))
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

func (dep *Dependencies) All() []string {
	var all []string
	for _, c := range dep.Components {
		all = append(all, c.Components()...)
	}
	return all
}

func (dep *Dependencies) Present(ctx context.Context, dir string) []string {
	var all []string
	for _, c := range dep.Components {
		for _, cc := range c.Components() {
			if !strings.Contains(cc, "*") {
				p := filepath.Join(dir, cc)
				exists, err := shared.Exists(ctx, p)
				if err != nil || !exists {
					continue
				}
			}
			all = append(all, cc)
		}
	}
	return all
}

func addFileHash(ctx context.Context, h io.Writer, path string) error {
	w := wool.Get(ctx).In("builders.addFileHash")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		err = f.Close()
		if err != nil {
			w.Error("cannot close hash file", wool.Field("path", f.Name()), wool.Field("error", err))
		}
	}(f)
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	return nil
}
