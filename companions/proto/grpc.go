package proto

import (
	"context"
	"embed"
	"fmt"
	"os"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

func GenerateGRPC(ctx context.Context, language languages.Language, destination string, service string, endpoints ...*basev0.Endpoint) error {
	w := wool.Get(ctx).In("generateGRPC", wool.Field("destination", destination))
	// call the companion
	image, err := CompanionImage(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get companion image")
	}
	// Create a tmp dir for the proto
	tmpDir, err := os.MkdirTemp("", "proto")
	if err != nil {
		return w.Wrapf(err, "cannot create tmp dir")
	}
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			w.Error("cannot remove tmp dir", wool.Field("path", path))
		}
	}(tmpDir)
	w.Debug("generating code from buf", wool.DirField(tmpDir), wool.DirField(destination))

	// Buf configuration
	err = CreateBufConfiguration(ctx, tmpDir, service, language)
	if err != nil {
		return w.Wrapf(err, "cannot create buf configuration")
	}
	// Add the proto stuff
	for _, endpoint := range endpoints {
		if grpc := resources.IsGRPC(ctx, endpoint); grpc != nil {
			unique := fmt.Sprintf("%s_%s", endpoint.Module, endpoint.Service)
			err = os.WriteFile(fmt.Sprintf("%s/%s_%s.proto", tmpDir, unique, endpoint.Name), grpc.Proto, 0600)
			if err != nil {
				return w.Wrapf(err, "cannot write proto file")
			}
		}
	}
	name := fmt.Sprintf("proto-%s-%d", service, time.Now().UnixMilli())
	runner, err := runners.NewDockerEnvironment(ctx, image, tmpDir, name)
	if err != nil {
		return w.Wrapf(err, "cannot create docker runner")
	}

	_, err = shared.CheckDirectoryOrCreate(ctx, destination)
	if err != nil {
		return w.Wrapf(err, "cannot create destination")
	}

	runner.WithMount(destination, "/workspace/output")
	runner.WithMount(tmpDir, "/workspace")
	runner.WithWorkDir("/workspace")
	runner.WithPause()

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
	return nil
}

type GoConfiguration struct {
	Destination     string
	GoPackagePrefix string
}

func CreateBufConfiguration(ctx context.Context, bufDir string, service string, language languages.Language) error {
	w := wool.Get(ctx).In("createBufConfiguration")
	switch language {
	case languages.GO:
		// Templatize
		err := templateGoConfiguration(ctx, bufDir, fmt.Sprintf("github.com/codefly-dev/cli/pkg/builder/clients/%s", service))
		if err != nil {
			return w.Wrapf(err, "cannot templatize")
		}
		return nil
	case languages.NotSupported:
		return w.NewError("language not supported")
	}
	return w.NewError("unknown language")
}

func templateGoConfiguration(ctx context.Context, bufDir string, goPackagePrefix string) error {
	w := wool.Get(ctx).In("templateGoConfiguration", wool.Field("bufDir", bufDir), wool.Field("goPackagePrefix", goPackagePrefix))
	templator := &templates.Templator{NameReplacer: templates.CutTemplateSuffix{}}
	conf := GoConfiguration{
		Destination:     "output",
		GoPackagePrefix: goPackagePrefix,
	}
	err := templator.CopyAndApply(ctx, goFS, "templates/go", bufDir, conf)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

// Embed

//go:embed templates/go
var goFS embed.FS
