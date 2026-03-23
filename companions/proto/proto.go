package proto

import (
	"context"
	"fmt"
	"os"
	"path"
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

	err = g.dependencies.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

func (g *Buf) WithCache(location string) {
	g.cache = location

}
