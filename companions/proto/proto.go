package proto

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/companion"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"
)

type Buf struct {
	Dir string

	// Keep the complete generation-input hash for caching. Buf configuration
	// and dependency-lock changes are just as generation-relevant as .proto
	// sources.
	dependencies *builders.Dependencies

	// internal cache for hash
	cache string

	// generatedDirs are generator-owned output roots removed immediately
	// before buf generate. Cleaning prevents package or service renames from
	// leaving stale generated Go packages in an otherwise green build.
	generatedDirs []string
}

func NewBuf(ctx context.Context, dir string) (*Buf, error) {
	w := wool.Get(ctx).In("proto.NewBuf")
	w.Debug("Creating new proto generator", wool.DirField(dir))
	deps := builders.NewDependencies("proto",
		builders.NewDependency(dir).WithPathSelect(shared.NewSelect(
			"*.proto",
			"buf.gen.yaml",
			"buf.yaml",
			"buf.lock",
		)),
	)
	deps.Localize(dir)
	return &Buf{
		Dir:          dir,
		dependencies: deps,
		cache:        dir,
	}, nil
}

// WithGeneratedDirs declares output directories that are wholly owned by Buf
// generation. Directories must be strict descendants of Dir; this invariant
// keeps regeneration cleanup scoped to the managed service.
func (g *Buf) WithGeneratedDirs(dirs ...string) *Buf {
	g.generatedDirs = append(g.generatedDirs, dirs...)
	return g
}

// Generate runs buf in a companion (golden wrapper) to regenerate code from local proto files.
func (g *Buf) Generate(ctx context.Context) error {
	w := wool.Get(ctx).In("proto.Generate")

	// Match cache
	g.dependencies.WithCache(g.cache)

	updated, err := g.dependencies.Updated(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if updated")
	}
	if !updated {
		w.Debug("no proto change detected")
		return nil
	}
	w.Info("detected changes to the proto: re-generating code", wool.DirField(g.Dir))

	var image *resources.DockerImage
	if dockerrun.DockerEngineRunning(ctx) {
		var imgErr error
		image, imgErr = CompanionImage(ctx)
		if imgErr != nil {
			w.Warn("cannot get companion image, falling back to local", wool.ErrField(imgErr))
		}
	}

	name := fmt.Sprintf("proto-%d", time.Now().UnixMilli())
	runner, err := companion.NewCompanionRunner(ctx, companion.CompanionOpts{
		Name:      name,
		SourceDir: g.Dir,
		Image:     image,
	})
	if err != nil {
		return w.Wrapf(err, "cannot create companion runner")
	}

	if runner.Backend() == companion.BackendDocker {
		runner.WithMount(g.Dir, "/workspace")
		runner.WithWorkDir("/workspace/proto")
	} else {
		runner.WithWorkDir(path.Join(g.Dir, "proto"))
	}
	runner.WithPause()

	defer func() {
		if shutErr := runner.Shutdown(ctx); shutErr != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(shutErr))
		}
	}()

	if err := runner.Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init runner")
	}

	proc, err := runner.NewProcess("buf", "dep", "update")
	if err != nil {
		return w.Wrapf(err, "cannot create process")
	}

	err = proc.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update buf")
	}

	if err := g.cleanGeneratedDirs(); err != nil {
		return w.Wrapf(err, "cannot clean stale generated output")
	}

	proc, err = runner.NewProcess("buf", "generate")
	if err != nil {
		return w.Wrapf(err, "cannot create process")
	}

	err = proc.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate with buf")
	}

	// Deal with OpenAPI if exists
	openapi := path.Join(g.Dir, "openapi/api.swagger.json")
	if ok, err := shared.FileExists(ctx, openapi); err == nil && ok {
		destination := path.Join(g.Dir, standards.OpenAPIPath)
		err = shared.CopyFile(ctx, openapi, destination)
		if err != nil {
			return w.Wrapf(err, "cannot copy file")
		}
		_ = os.Remove(openapi)
	}

	// Generate TypeScript types from OpenAPI spec if swagger files exist.
	// Pipeline: Swagger 2.0 → OpenAPI 3.0 (swagger2openapi) → TypeScript (openapi-typescript)
	openapiDir := path.Join(g.Dir, "openapi")
	if entries, dirErr := os.ReadDir(openapiDir); dirErr == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".swagger.json") {
				continue
			}

			var containerSwagger, containerV3, containerTS string
			if runner.Backend() == companion.BackendDocker {
				containerSwagger = filepath.Join("/workspace/openapi", entry.Name())
				containerV3 = strings.TrimSuffix(containerSwagger, ".swagger.json") + ".openapi3.json"
				containerTS = strings.TrimSuffix(containerSwagger, ".swagger.json") + ".ts"
			} else {
				containerSwagger = filepath.Join(openapiDir, entry.Name())
				containerV3 = strings.TrimSuffix(containerSwagger, ".swagger.json") + ".openapi3.json"
				containerTS = strings.TrimSuffix(containerSwagger, ".swagger.json") + ".ts"
			}

			// Convert Swagger 2.0 → OpenAPI 3.0
			convProc, convErr := runner.NewProcess("swagger2openapi", containerSwagger, "-o", containerV3)
			if convErr != nil {
				w.Debug("cannot create swagger2openapi process", wool.ErrField(convErr))
				continue
			}
			if convErr = convProc.Run(ctx); convErr != nil {
				w.Debug("swagger2openapi conversion failed (non-fatal)", wool.ErrField(convErr))
				continue
			}

			// Generate TypeScript types from OpenAPI 3.0
			tsProc, tsErr := runner.NewProcess("npx", "openapi-typescript", containerV3, "-o", containerTS)
			if tsErr != nil {
				w.Debug("cannot create openapi-typescript process", wool.ErrField(tsErr))
				continue
			}
			if tsErr = tsProc.Run(ctx); tsErr != nil {
				w.Debug("TS type generation failed (non-fatal)", wool.ErrField(tsErr))
			} else {
				w.Info("generated TypeScript types", wool.Field("output", containerTS))
			}

			// Clean up intermediate file
			v3File := filepath.Join(openapiDir, strings.TrimSuffix(entry.Name(), ".swagger.json")+".openapi3.json")
			_ = os.Remove(v3File)
		}
	}

	err = g.dependencies.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

func (g *Buf) cleanGeneratedDirs() error {
	root, err := filepath.Abs(g.Dir)
	if err != nil {
		return fmt.Errorf("resolve generator root: %w", err)
	}
	for _, dir := range g.generatedDirs {
		output, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve generated directory %q: %w", dir, err)
		}
		rel, err := filepath.Rel(root, output)
		if err != nil {
			return fmt.Errorf("scope generated directory %q: %w", dir, err)
		}
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generated directory %q is not a strict descendant of %q", output, root)
		}
		if err := os.RemoveAll(output); err != nil {
			return fmt.Errorf("remove generated directory %q: %w", output, err)
		}
	}
	return nil
}

func (g *Buf) WithCache(location string) {
	g.cache = location

}
