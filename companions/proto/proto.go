package proto

import (
	"context"
	"os"
	"path"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/configurations/standards"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
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

// Generate relies on local buf files
func (g *Buf) Generate(ctx context.Context) error {
	w := wool.Get(ctx).In("proto.Generate")

	// Match cache
	g.dependencies.WithCache(g.cache)

	if !runners.DockerEngineRunning(ctx) {
		return w.NewError("docker is not running")
	}

	updated, err := g.dependencies.Updated(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if updated")
	}
	if !updated {
		w.Debug("no proto change detected")
		return nil
	}
	w.Info("detected changes to the proto: re-generating code", wool.DirField(g.Dir))
	image, err := CompanionImage(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get companion image")
	}

	runner, err := runners.NewDocker(ctx, image)
	if err != nil {
		return w.Wrapf(err, "cannot create docker runner")
	}
	runner.WithMount(g.Dir, "/workspace")
	runner.WithWorkDir("/workspace/proto")

	runner.WithCommand("buf", "mod", "update")
	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update buf")
	}

	runner.WithCommand("buf", "generate")
	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}

	// Move the result
	openapi := path.Join(g.Dir, "openapi/api.swagger.json")
	destination := path.Join(g.Dir, standards.OpenAPIPath)
	err = shared.CopyFile(ctx, openapi, destination)
	if err != nil {
		return w.Wrapf(err, "cannot copy file")
	}
	_ = os.Remove(openapi)

	err = g.dependencies.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

func (g *Buf) WithCache(location string) {
	g.cache = location

}
