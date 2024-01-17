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

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/shared"
)

type Dependency struct {
	Name       string
	Components []string
	Ignore     shared.Ignore
	Select     shared.Select

	dir string
}

func NewDependency(name string, components ...string) *Dependency {
	return &Dependency{
		Name:       name,
		Components: components,
	}
}

func (dep *Dependency) WithDir(dir string) *Dependency {
	dep.dir = dir
	return dep
}

func (dep *Dependency) WithIgnore(ignore shared.Ignore) *Dependency {
	dep.Ignore = ignore
	return dep
}

func (dep *Dependency) WithSelect(sel shared.Select) *Dependency {
	dep.Select = sel
	return dep
}

func (dep *Dependency) Updated(ctx context.Context) (bool, error) {
	hash, err := dep.Hash(ctx)
	if err != nil {
		return true, err
	}
	current := dep.LoadHash()
	if current == hash {
		return false, nil
	}
	err = dep.WriteHash(hash)
	if err != nil {
		return true, err
	}

	return true, nil
}

func (dep *Dependency) hashFile() string {
	return filepath.Join(dep.dir, fmt.Sprintf(".%s.hash", strings.ToLower(dep.Name)))
}

func (dep *Dependency) WriteHash(hash string) error {
	if dep.dir == "" {
		return nil
	}
	// New or overwrite
	f, err := os.Create(dep.hashFile())
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s", hash)
	if err != nil {
		return err
	}
	return nil
}

func (dep *Dependency) LoadHash() string {
	if dep.dir == "" {
		return configurations.Unknown
	}
	f, err := os.Open(dep.hashFile())
	if err != nil {
		return ""
	}
	defer f.Close()
	var hash string
	_, err = fmt.Fscanf(f, "%s", &hash)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(hash)
}

func (dep *Dependency) Hash(_ context.Context) (string, error) {
	h := sha256.New()
	for _, path := range dep.Components {
		// If relative path, use dir
		if !filepath.IsAbs(path) {
			path = filepath.Join(dep.dir, path)
		}
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Skip directories
				return nil
			}
			// Ignore hash themselves
			if strings.HasSuffix(path, ".hash") {
				return nil
			}
			if dep.Ignore != nil && dep.Ignore.Skip(path) {
				return nil
			}
			if dep.Select != nil && !dep.Select.Keep(path) {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(h, file); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
