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
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"
)

type Buf struct {
	Dir string

	// Keep the proto hash for cashing
	dependencies *builders.Dependencies

	// internal cache for hash
	cache string
}

func NewBuf(ctx context.Context, dir string) (*Buf, error) {
	w := wool.Get(ctx).In("proto.NewBuf")
	w.Debug("Creating new proto generator", wool.DirField(dir))
	deps := builders.NewDependencies("proto",
		builders.NewDependency(dir).WithPathSelect(shared.NewSelect("*.proto")),
	)
	deps.Localize(dir)
	return &Buf{
		Dir:          dir,
		dependencies: deps,
		cache:        dir,
	}, nil
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
	if runners.DockerEngineRunning(ctx) {
		var imgErr error
		image, imgErr = CompanionImage(ctx)
		if imgErr != nil {
			w.Warn("cannot get companion image, falling back to local", wool.ErrField(imgErr))
		}
	}

	name := fmt.Sprintf("proto-%d", time.Now().UnixMilli())
	runner, err := runners.NewCompanionRunner(ctx, runners.CompanionOpts{
		Name:      name,
		SourceDir: g.Dir,
		Image:     image,
	})
	if err != nil {
		return w.Wrapf(err, "cannot create companion runner")
	}

	if runner.Backend() == runners.BackendDocker {
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
	if ok, _ := shared.FileExists(ctx, openapi); err == nil && ok {
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
			if runner.Backend() == runners.BackendDocker {
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

func (g *Buf) WithCache(location string) {
	g.cache = location

}
