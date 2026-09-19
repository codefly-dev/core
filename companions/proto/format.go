package proto

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"
)

// The Go the plugins emit is not the Go consumers commit.
//
// protoc-gen-go and protoc-gen-go-grpc write their imports sorted in one block,
// stdlib in among the third-party paths; protoc-gen-grpc-gateway imports a
// sibling package by bare path even when that package's name is not its last
// path element. Every consumer of this companion runs goimports over that
// output before committing it — the stdlib group split off with a blank line,
// the alias (`jobsv1 "…/jobs/v1"`) added — and gates its checked-in bindings
// on that shape. A companion that stops at `buf generate` therefore reproduces
// a committed tree only up to formatting, which is a 107-file diff on a
// service of any size and reads, wrongly, as toolchain drift.
//
// So the formatting pass is part of generation, and it runs where generation
// runs: inside the companion, with the pinned goimports the image bakes. A
// consumer needs nothing on the host to get the committed shape back.

// GoOutputRoots returns every output directory the buf template at
// templateDir/templateName declares that holds generated Go, as absolute host
// paths. A relative `out` is resolved against templateDir, which is where buf
// resolves it. Directories the template names that do not exist or hold no
// Go — a TypeScript or OpenAPI output — are not roots.
func GoOutputRoots(templateDir, templateName string) ([]string, error) {
	contents, err := os.ReadFile(filepath.Join(templateDir, templateName))
	if err != nil {
		return nil, fmt.Errorf("read generation template: %w", err)
	}
	var document struct {
		Plugins []struct {
			Out string `yaml:"out"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("parse generation template: %w", err)
	}

	seen := make(map[string]struct{}, len(document.Plugins))
	for _, plugin := range document.Plugins {
		out := strings.TrimSpace(plugin.Out)
		if out == "" {
			continue
		}
		root := filepath.Clean(out)
		if !filepath.IsAbs(root) {
			root = filepath.Join(templateDir, root)
		}
		hasGo, err := containsGoFile(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect output %s: %w", root, err)
		}
		if hasGo {
			seen[root] = struct{}{}
		}
	}

	roots := make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}

func containsGoFile(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found || entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// ProcessRunner is what the formatting pass needs from a companion: the
// ability to start a process in it. Both the CompanionRunner the generator
// drives and the bare Docker environment the CLI drives satisfy it, so the
// pass is one function rather than one per caller.
type ProcessRunner interface {
	NewProcess(bin string, args ...string) (base.Proc, error)
}

// FormatGoOutputs runs goimports over every Go output root the template
// declares, inside the companion.
//
// hostRoot is the host directory the companion mounts at containerRoot; each
// root is reached through that mapping. For a backend that runs on the host
// (Nix, local) containerRoot is empty and roots are used as they are.
//
// goimports is run from inside each root rather than pointed at it from
// elsewhere: it resolves module-local import paths from its working directory,
// and the alias it adds for a package whose name is not its path's last
// element depends on that resolution. Run from outside the module it emits no
// alias and the output still differs from what consumers commit.
func FormatGoOutputs(ctx context.Context, runner ProcessRunner, templateDir, templateName, hostRoot, containerRoot string) error {
	w := wool.Get(ctx).In("proto.FormatGoOutputs", wool.DirField(templateDir))

	roots, err := GoOutputRoots(templateDir, templateName)
	if err != nil {
		return w.Wrapf(err, "cannot discover generated Go outputs")
	}
	if len(roots) == 0 {
		w.Debug("no generated Go to format")
		return nil
	}

	for _, root := range roots {
		dir := root
		if containerRoot != "" {
			rel, err := filepath.Rel(hostRoot, root)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				// A root outside the mount is not visible to the companion, and
				// formatting it on the host is exactly the step this exists to
				// remove. The template is wrong, not the mount.
				return w.NewError("generated Go output %s lies outside the companion mount %s; every `out` in %s must be under it", root, hostRoot, filepath.Join(templateDir, templateName))
			}
			dir = path.Join(containerRoot, filepath.ToSlash(rel))
		}
		w.Info("goimports", wool.Field("dir", dir))
		proc, err := runner.NewProcess("goimports", "-w", ".")
		if err != nil {
			return w.Wrapf(err, "cannot create goimports process")
		}
		proc.WithDir(dir)
		if err := proc.Run(ctx); err != nil {
			return w.Wrapf(err, "goimports failed in %s", dir)
		}
	}
	return nil
}
