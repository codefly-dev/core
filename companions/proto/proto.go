package proto

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type Proto struct {
	Dir string

	// Keep the proto hash for cashing
	dependencies *builders.Dependencies

	// internal cache for hash
	cache string
}

func NewProto(ctx context.Context, dir string) (*Proto, error) {
	w := wool.Get(ctx).In("proto.NewProto")
	w.Debug("Creating new proto generator", wool.DirField(dir))
	deps := builders.NewDependencies("proto",
		builders.NewDependency(dir).WithPathSelect(shared.NewSelect("*.proto")),
	)
	deps.Localize(dir)
	return &Proto{
		Dir:          dir,
		dependencies: deps,
		cache:        dir,
	}, nil
}

func (g *Proto) Generate(ctx context.Context) error {
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
	volume := fmt.Sprintf("%s:/workspace", g.Dir)

	runner, err := runners.NewProcess(ctx, "docker", "run", "--rm", "-v", volume, "-w", "/workspace/proto", image, "buf", "mod", "update")
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	runner.WithDir(g.Dir)
	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}

	runner, err = runners.NewProcess(ctx, "docker", "run", "--rm", "-v", volume, "-w", "/workspace/proto", image, "buf", "generate")
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	runner.WithDir(g.Dir)
	w.Debug("generating code from buf", wool.DirField(g.Dir))

	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}

	err = g.dependencies.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

func (g *Proto) WithCache(location string) {
	g.cache = location

}
