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
	default:
		return w.Wrapf(err, "language not supported")
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
	runner, err := runners.NewDockerEnvironment(ctx, image, root, name)
	if err != nil {
		return w.Wrapf(err, "cannot create docker runner")
	}
	runner.WithMount(openapiDir, "/workspace/openapi")
	runner.WithMount(root, "/workspace")
	runner.WithWorkDir("/workspace")
	defer func() {
		err = runner.Shutdown(ctx)
		if err != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(err))
		}
	}()

	err = runner.Init(ctx)
	if err != nil {
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
