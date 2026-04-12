package proto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// GenerateOpenAPI runs the OpenAPI generator in a companion (golden wrapper)
// to produce client code for the given language.
func GenerateOpenAPI(ctx context.Context, language languages.Language, destination string, _ string, endpoints ...*basev0.Endpoint) error {
	w := wool.Get(ctx).In("generateOpenAPI", wool.Field("destination", destination))
	_, err := shared.CheckDirectoryOrCreate(ctx, destination)
	if err != nil {
		return w.Wrapf(err, "can't create directory for destination")
	}
	// call the companion
	image, err := CompanionImage(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get companion image")
	}
	// Create a tmp dir for the proto
	openapiDir, err := os.MkdirTemp("", "openapi")
	if err != nil {
		return w.Wrapf(err, "cannot create tmp dir")
	}
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			w.Error("cannot remove tmp dir", wool.Field("path", path))
		}
	}(openapiDir)

	// Add the proto stuff
	var files []string
	var file string
	var unique string
	for _, endpoint := range endpoints {
		if rest := resources.IsRest(ctx, endpoint); rest != nil {
			unique = fmt.Sprintf("%s_%s", endpoint.Module, endpoint.Service)
			file = fmt.Sprintf("%s_%s.rest", unique, endpoint.Name)
			files = append(files, file)
			err = os.WriteFile(filepath.Join(openapiDir, file), rest.Openapi, 0600)
			if err != nil {
				return w.Wrapf(err, "cannot write open api file")
			}
		}
	}
	w.Debug("generating code", wool.SliceCountField(files), wool.DirField(destination))
	if len(files) == 0 {
		return w.NewError("no files to generate")
	}
	if len(files) > 1 {
		return w.NewError("cannot generate code from multiple files")
	}
	switch language {
	case languages.GO:
		return generateOpenAPIGo(ctx, unique, image, destination, openapiDir, file)
	case languages.TYPESCRIPT:
		return generateOpenAPITypeScript(ctx, unique, image, destination, openapiDir, file)
	default:
		return w.NewError("language not supported")
	}
}

func generateOpenAPIGo(ctx context.Context, unique string, image *resources.DockerImage, destinationDir string, openapiDir, file string) error {
	w := wool.Get(ctx).In("generateOpenAPIGo", wool.Field("destinationDir", destinationDir))
	w.Info("generating openapi go client", wool.Field("file", file))
	// We need to go back to the root to find the go mod and mount this as a volume

	root, err := findModRoot(destinationDir)
	if err != nil {
		return w.Wrapf(err, "cannot find go.mod")
	}
	target, err := filepath.Rel(root, destinationDir)
	if err != nil {
		return w.Wrapf(err, "cannot find relative path")
	}

	openapiFile := filepath.Join("/workspace/openapi", file)

	name := fmt.Sprintf("openapi-%s-%d", unique, time.Now().UnixMilli())
	runner, err := runners.NewCompanionRunner(ctx, runners.CompanionOpts{
		Name:      name,
		SourceDir: root,
		Image:     image,
	})
	if err != nil {
		return w.Wrapf(err, "cannot create companion runner")
	}
	runner.WithMount(openapiDir, "/workspace/openapi")
	runner.WithMount(root, "/workspace")
	runner.WithWorkDir("/workspace")
	defer func() {
		if shutErr := runner.Shutdown(ctx); shutErr != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(shutErr))
		}
	}()

	if err := runner.Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init runner")
	}

	proc, err := runner.NewProcess(
		"swagger",
		"generate",
		"client",
		"-f",
		openapiFile,
		"-A",
		unique,
		"--target",
		target,
	)
	if err != nil {
		return w.Wrapf(err, "cannot create process")
	}
	err = proc.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	return nil
}

// generateOpenAPITypeScript converts a Swagger 2.0 spec to OpenAPI 3.0 and
// then generates TypeScript types using openapi-typescript — all inside the
// proto companion container.
func generateOpenAPITypeScript(ctx context.Context, unique string, image *resources.DockerImage, destinationDir string, openapiDir, file string) error {
	w := wool.Get(ctx).In("generateOpenAPITypeScript", wool.Field("destinationDir", destinationDir))
	w.Info("generating openapi typescript types", wool.Field("file", file))

	openapiFile := filepath.Join("/workspace/openapi", file)
	v3File := "/workspace/openapi/openapi3.json"
	tsFile := "/workspace/output/api.d.ts"

	name := fmt.Sprintf("openapi-ts-%s-%d", unique, time.Now().UnixMilli())
	runner, err := runners.NewCompanionRunner(ctx, runners.CompanionOpts{
		Name:      name,
		SourceDir: openapiDir,
		Image:     image,
	})
	if err != nil {
		return w.Wrapf(err, "cannot create companion runner")
	}
	runner.WithMount(openapiDir, "/workspace/openapi")
	runner.WithMount(destinationDir, "/workspace/output")
	runner.WithWorkDir("/workspace")
	runner.WithPause()

	defer func() {
		if shutErr := runner.Shutdown(ctx); shutErr != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(shutErr))
		}
	}()

	if err := runner.Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init runner")
	}

	// Step 1: Convert Swagger 2.0 → OpenAPI 3.0
	proc, err := runner.NewProcess("swagger2openapi", openapiFile, "-o", v3File)
	if err != nil {
		return w.Wrapf(err, "cannot create swagger2openapi process")
	}
	if err := proc.Run(ctx); err != nil {
		return w.Wrapf(err, "swagger2openapi conversion failed")
	}

	// Step 2: Generate TypeScript types from OpenAPI 3.0
	proc, err = runner.NewProcess("npx", "openapi-typescript", v3File, "-o", tsFile)
	if err != nil {
		return w.Wrapf(err, "cannot create openapi-typescript process")
	}
	if err := proc.Run(ctx); err != nil {
		return w.Wrapf(err, "openapi-typescript generation failed")
	}

	return nil
}

func findModRoot(destination string) (string, error) {
	root := destination
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root, nil
		}
		root = filepath.Dir(root)
		if root == "/" || root == "." {
			return "", fmt.Errorf("cannot find go.mod")
		}
	}
}
