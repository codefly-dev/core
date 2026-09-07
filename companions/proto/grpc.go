package proto

import (
	"context"
	"embed"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/companion"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

// GenerateGRPC runs buf in a companion (Docker/Nix/local via golden wrapper)
// to generate gRPC client code for the given language and endpoints. It is a
// thin wrapper over GenerateClient: the proto sources come from the endpoints
// and no facade is emitted.
func GenerateGRPC(ctx context.Context, language languages.Language, destination string, service string, endpoints ...*basev0.Endpoint) error {
	w := wool.Get(ctx).In("generateGRPC", wool.Field("destination", destination))
	var sources []Source
	for _, endpoint := range endpoints {
		if grpc := resources.IsGRPC(ctx, endpoint); grpc != nil {
			// The file name is module/service-scoped so distinct endpoints
			// never collide and the generated stub filenames stay stable.
			name := fmt.Sprintf("%s_%s_%s.proto", endpoint.Module, endpoint.Service, endpoint.Name)
			sources = append(sources, Source{Path: name, Content: grpc.Proto})
		}
	}
	if len(sources) == 0 {
		return w.NewError("no gRPC endpoints to generate")
	}
	return generateClient(ctx, clientSpec{
		language:    language,
		destination: destination,
		service:     service,
		sources:     sources,
	})
}

// FacadeOptions configures the gateway-bound client facade plugins.
type FacadeOptions struct {
	// Facade turns on the protoc-gen-codefly-facade-<lang> plugin.
	Facade bool
	// Services restricts the facade to the listed services (empty → all).
	Services []string
	// Module overrides the facade entry-point name (default: last non-version
	// segment of the proto package).
	Module string
}

func (f FacadeOptions) services() string { return strings.Join(f.Services, "+") }

// outputDir is the buf `out` directory the templates render against; it is
// mounted to the destination in the companion.
const outputDir = "output"

type GoConfiguration struct {
	Destination     string
	GoPackagePrefix string
	Facade          bool
	Services        string
	Module          string
}

type PythonConfiguration struct {
	Destination string
	Facade      bool
	Services    string
	Module      string
}

type TypeScriptConfiguration struct {
	Destination string
	Facade      bool
	Services    string
	Module      string
}

type RustConfiguration struct {
	Destination string
}

func CreateBufConfiguration(ctx context.Context, bufDir string, service string, language languages.Language, facade FacadeOptions) error {
	w := wool.Get(ctx).In("createBufConfiguration")
	switch language {
	case languages.GO:
		err := templateGoConfiguration(ctx, bufDir, fmt.Sprintf("github.com/codefly-dev/cli/pkg/builder/clients/%s", service), facade)
		if err != nil {
			return w.Wrapf(err, "cannot templatize")
		}
		return nil
	case languages.PYTHON:
		err := templatePythonConfiguration(ctx, bufDir, facade)
		if err != nil {
			return w.Wrapf(err, "cannot templatize")
		}
		return nil
	case languages.TYPESCRIPT:
		err := templateTypeScriptConfiguration(ctx, bufDir, facade)
		if err != nil {
			return w.Wrapf(err, "cannot templatize")
		}
		return nil
	case languages.RUST:
		err := templateRustConfiguration(ctx, bufDir)
		if err != nil {
			return w.Wrapf(err, "cannot templatize")
		}
		return nil
	case languages.NotSupported:
		return w.NewError("language not supported")
	}
	return w.NewError("unknown language")
}

func templateGoConfiguration(ctx context.Context, bufDir string, goPackagePrefix string, facade FacadeOptions) error {
	w := wool.Get(ctx).In("templateGoConfiguration", wool.Field("bufDir", bufDir), wool.Field("goPackagePrefix", goPackagePrefix))
	templator := &templates.Templator{NameReplacer: templates.CutTemplateSuffix{}}
	conf := GoConfiguration{
		Destination:     outputDir,
		GoPackagePrefix: goPackagePrefix,
		Facade:          facade.Facade,
		Services:        facade.services(),
		Module:          facade.Module,
	}
	err := templator.CopyAndApply(ctx, goFS, "templates/go", bufDir, conf)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

func templatePythonConfiguration(ctx context.Context, bufDir string, facade FacadeOptions) error {
	w := wool.Get(ctx).In("templatePythonConfiguration", wool.Field("bufDir", bufDir))
	templator := &templates.Templator{NameReplacer: templates.CutTemplateSuffix{}}
	conf := PythonConfiguration{
		Destination: outputDir,
		Facade:      facade.Facade,
		Services:    facade.services(),
		Module:      facade.Module,
	}
	err := templator.CopyAndApply(ctx, pythonFS, "templates/python", bufDir, conf)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

func templateTypeScriptConfiguration(ctx context.Context, bufDir string, facade FacadeOptions) error {
	w := wool.Get(ctx).In("templateTypeScriptConfiguration", wool.Field("bufDir", bufDir))
	templator := &templates.Templator{NameReplacer: templates.CutTemplateSuffix{}}
	conf := TypeScriptConfiguration{
		Destination: outputDir,
		Facade:      facade.Facade,
		Services:    facade.services(),
		Module:      facade.Module,
	}
	err := templator.CopyAndApply(ctx, typescriptFS, "templates/typescript", bufDir, conf)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

func templateRustConfiguration(ctx context.Context, bufDir string) error {
	w := wool.Get(ctx).In("templateRustConfiguration", wool.Field("bufDir", bufDir))
	templator := &templates.Templator{NameReplacer: templates.CutTemplateSuffix{}}
	conf := RustConfiguration{
		Destination: outputDir,
	}
	err := templator.CopyAndApply(ctx, rustFS, "templates/rust", bufDir, conf)
	if err != nil {
		return w.Wrapf(err, "cannot copy and apply template")
	}
	return nil
}

// runBuf sets up the proto companion over a prepared temp dir and runs buf.
// before, when set, runs after the runner is initialized but before buf
// generate (used by the Python facade path to strip the descriptor image).
func runBuf(ctx context.Context, name string, image *resources.DockerImage, tmpDir, destination string, depUpdate bool, generateArgs []string, before func(context.Context, companion.CompanionRunner) error) error {
	w := wool.Get(ctx).In("runBuf", wool.DirField(tmpDir), wool.DirField(destination))

	runner, err := companion.NewCompanionRunner(ctx, companion.CompanionOpts{
		Name:      name,
		SourceDir: tmpDir,
		Image:     image,
	})
	if err != nil {
		return w.Wrapf(err, "cannot create companion runner")
	}

	runner.WithMount(destination, "/workspace/output")
	runner.WithMount(tmpDir, "/workspace")
	runner.WithWorkDir("/workspace")
	runner.WithUser(hostUserSpec())
	runner.WithPause()

	defer func() {
		if shutErr := runner.Shutdown(ctx); shutErr != nil {
			w.Warn("cannot shutdown runner", wool.ErrField(shutErr))
		}
	}()

	if err = runner.Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init runner")
	}

	var proc base.Proc
	if depUpdate {
		if proc, err = runner.NewProcess("buf", "dep", "update"); err != nil {
			return w.Wrapf(err, "cannot create process")
		}
		if err = proc.Run(ctx); err != nil {
			return w.Wrapf(err, "cannot update buf")
		}
	}

	if before != nil {
		if err = before(ctx, runner); err != nil {
			return w.Wrapf(err, "cannot run pre-generate step")
		}
	}

	if proc, err = runner.NewProcess("buf", generateArgs...); err != nil {
		return w.Wrapf(err, "cannot create process")
	}
	if err = proc.Run(ctx); err != nil {
		return w.Wrapf(err, "cannot generate with buf")
	}
	return nil
}

// Embed

//go:embed templates/go
var goFS embed.FS

//go:embed templates/python
var pythonFS embed.FS

//go:embed templates/typescript
var typescriptFS embed.FS

//go:embed templates/rust
var rustFS embed.FS
