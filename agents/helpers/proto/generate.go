package proto

import (
	"context"
	"embed"
	"fmt"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type Proto struct {
	Dir     string
	version string

	// Keep the proto hash for cashing
	dependencies *builders.Dependencies
}

func NewProto(ctx context.Context, dir string) (*Proto, error) {
	w := wool.Get(ctx).In("proto.NewProto")
	v, err := version(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get v")
	}
	deps := builders.NewDependencies("proto",
		builders.NewDependency(dir).WithPathSelect(shared.NewSelect("*.proto")),
	)
	deps.Localize(dir)

	return &Proto{
		Dir:          dir,
		version:      v,
		dependencies: deps,
	}, nil
}

func version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("proto.version")
	conf, err := configurations.LoadFromFs[configurations.Info](shared.Embed(info))
	if err != nil {
		return "", w.Wrapf(err, "cannot load info for companion")
	}
	// check we have a valid semantic version
	v, err := semver.NewVersion(conf.Version)
	if err != nil {
		return "", w.Wrapf(err, "cannot parse version <%s>", conf.Version)
	}
	return v.String(), nil
}

func CompanionImage(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("proto.CompanionImage")
	v, err := version(ctx)
	if err != nil {
		return "", w.Wrapf(err, "cannot get version")
	}
	return fmt.Sprintf("codeflydev/companion:%s", v), nil
}

//go:embed info.codefly.yaml
var info embed.FS

func (g *Proto) Generate(ctx context.Context) error {
	w := wool.Get(ctx).In("proto.Generate")
	// Check if Docker is running
	if !runners.DockerRunning(ctx) {
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
	runner, err := runners.NewRunner(ctx, "docker", "run", "--rm", "-v", volume, "-w", "/workspace/proto", image, "buf", "mod", "update")
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	runner.WithDir(g.Dir)
	err = runner.Run()
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	runner, err = runners.NewRunner(ctx, "docker", "run", "--rm", "-v", volume, "-w", "/workspace/proto", image, "buf", "generate")
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	runner.WithDir(g.Dir)
	w.Debug("Generating code from buf", wool.DirField(g.Dir))
	err = runner.Run()
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	err = g.dependencies.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update cache")
	}
	return nil
}

//func (g *Proto) Valid() bool {
//	runner, err := runners.NewDocker(context.Background())
//	if err != nil {
//		return false
//	}
//	runner.WithWorkDir(path.Join(g.Dir, "proto")
//	runner.WithCommand("buf", "generate")
//	err = runner.Run()
//
//}
