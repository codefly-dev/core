package proto

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
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

	// generatedDirs are generator-owned output roots emptied immediately
	// before buf generate. Cleaning prevents package or service renames from
	// leaving stale generated Go packages in an otherwise green build. The
	// roots themselves remain present because local protoc plugins require
	// their configured output directories to exist.
	generatedDirs []string

	// generatedRoot is the service boundary that contains every generated
	// directory. It defaults to Dir, but a service whose protocol tree lives
	// below the service root can widen this boundary explicitly.
	generatedRoot string

	// skipOpenAPI opts the generator out of the OpenAPI post-generation stage.
	// A service that owns a proto tree purely for gRPC stubs but also ships an
	// openapi/ REST contract (e.g. a Python FastAPI service) would otherwise
	// have TypeScript types generated into it on every Sync. WithoutOpenAPI
	// suppresses that stage; buf code generation is unaffected.
	skipOpenAPI bool
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
		Dir:           dir,
		dependencies:  deps,
		cache:         dir,
		generatedRoot: dir,
	}, nil
}

// hostUserSpec returns the "UID:GID" a proto companion should run as, or ""
// to keep the image's default user. On Linux the container otherwise runs as
// root and writes the generated tree as root into the bind mount, leaving it
// unreadable by a non-root host such as a CI runner. It is empty off Linux:
// Docker Desktop on macOS already remaps bind-mount ownership to the host
// user, and the non-Docker backends run as the host user to begin with.
func hostUserSpec() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// WithGeneratedRoot declares the service root that owns generated outputs.
// This is distinct from Dir for nested protocol layouts such as code/proto:
// Buf runs from code/, while generated OpenAPI may still live at the service
// root. cleanGeneratedDirs continues to reject every path outside this root.
func (g *Buf) WithGeneratedRoot(root string) *Buf {
	g.generatedRoot = root
	return g
}

// WithGeneratedDirs declares output directories that are wholly owned by Buf
// generation. Directories must be strict descendants of the generated root;
// this invariant keeps regeneration cleanup scoped to the managed service.
func (g *Buf) WithGeneratedDirs(dirs ...string) *Buf {
	g.generatedDirs = append(g.generatedDirs, dirs...)
	return g
}

// WithoutOpenAPI opts out of the OpenAPI post-generation stage: the canonical
// OpenAPI move and the OpenAPI→TypeScript derivation are both skipped, leaving
// any openapi/*.swagger.json inputs untouched. buf generate still runs. This
// suits non-TypeScript services that own a proto tree for gRPC stubs yet keep
// an unrelated openapi/ REST contract alongside it (e.g. service-python-fastapi).
//
// The opt-out only stops future emission; it deliberately does not delete
// artifacts a prior (opted-in) run already produced. A service that adopts this
// after previously syncing with the stage enabled must remove the now-stale
// generated files (e.g. a committed openapi/api.ts) once by hand — the stage
// cannot tell a stale generated .ts from one someone later authored at the same
// path, so it never removes them.
func (g *Buf) WithoutOpenAPI() *Buf {
	g.skipOpenAPI = true
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
		runner.WithUser(hostUserSpec())
	} else {
		runner.WithWorkDir(path.Join(g.Dir, "proto"))
	}
	runner.WithPause()

	defer func() {
		if shutErr := runner.Shutdown(ctx); shutErr != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(shutErr))
		}
	}()

	// Prepare output roots before a Docker companion bind-mounts the source
	// tree. Removing and recreating directories after container initialization
	// can leave Docker Desktop with stale directory inodes, causing local
	// plugins to fail while opening otherwise valid nested output paths.
	if err := g.cleanGeneratedDirs(); err != nil {
		return w.Wrapf(err, "cannot clean stale generated output")
	}

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

	if err = g.emitOpenAPIArtifacts(ctx, runner); err != nil {
		return w.Wrapf(err, "cannot emit OpenAPI artifacts")
	}

	if err = g.updateGenerationCache(ctx); err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

// emitOpenAPIArtifacts runs the OpenAPI post-generation stage that follows buf
// generate: it promotes a generated Swagger document to Codefly's canonical
// OpenAPI path, then derives TypeScript types from every openapi/*.swagger.json
// via the Swagger 2.0 → OpenAPI 3.0 → TypeScript pipeline. Callers that own a
// proto tree purely for gRPC stubs but keep an unrelated openapi/ REST contract
// opt out with WithoutOpenAPI, which short-circuits this stage and leaves those
// swagger inputs untouched.
func (g *Buf) emitOpenAPIArtifacts(ctx context.Context, runner companion.CompanionRunner) error {
	w := wool.Get(ctx).In("proto.emitOpenAPIArtifacts")

	if g.skipOpenAPI {
		w.Debug("skipping OpenAPI post-generation stage (opted out)")
		return nil
	}

	// Deal with OpenAPI if exists
	openapi := path.Join(g.Dir, "openapi/api.swagger.json")
	if ok, err := shared.FileExists(ctx, openapi); err == nil && ok {
		destination := path.Join(g.Dir, standards.OpenAPIPath)
		if err = moveGeneratedOpenAPI(ctx, openapi, destination); err != nil {
			return w.Wrapf(err, "cannot copy file")
		}
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

	return nil
}

// updateGenerationCache hashes inputs after generation. `buf dep update` may
// rewrite buf.lock, so persisting the pre-generation hash would force one
// unnecessary second generation and another round of BSR requests.
func (g *Buf) updateGenerationCache(ctx context.Context) error {
	if _, err := g.dependencies.Updated(ctx); err != nil {
		return err
	}
	return g.dependencies.UpdateCache(ctx)
}

// moveGeneratedOpenAPI preserves the generated document when the generator's
// output and Codefly's canonical OpenAPI path are the same file. The two paths
// used to differ; after standards.OpenAPIPath converged on
// openapi/api.swagger.json, blindly copying and then removing the source
// deleted the canonical artifact after every successful generation.
func moveGeneratedOpenAPI(ctx context.Context, source, destination string) error {
	if filepath.Clean(source) == filepath.Clean(destination) {
		return nil
	}
	if err := shared.CopyFile(ctx, source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}

func (g *Buf) cleanGeneratedDirs() error {
	root, err := filepath.Abs(g.generatedRoot)
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
		if err := os.MkdirAll(output, 0o755); err != nil {
			return fmt.Errorf("recreate generated directory %q: %w", output, err)
		}
	}
	return nil
}

func (g *Buf) WithCache(location string) {
	g.cache = location

}
