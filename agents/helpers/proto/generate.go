package proto

import (
	"context"
	"embed"
	"fmt"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type Proto struct {
	Dir     string
	version string
}

func NewProto(ctx context.Context, dir string) (*Proto, error) {
	w := wool.Get(ctx).In("proto.NewProto")
	version, err := version(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	return &Proto{
		Dir:     dir,
		version: version,
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

//go:embed info.codefly.yaml
var info embed.FS

func (g *Proto) Generate(ctx context.Context) error {
	w := wool.Get(ctx).In("proto.Generate")
	image := fmt.Sprintf("codeflydev/companion:%s", g.version)
	volume := fmt.Sprintf("%s:/workspace", g.Dir)
	runner := runners.Runner{Dir: g.Dir, Bin: "docker", Args: []string{"run", "--rm", "-v", volume, image, "buf", "generate"}}
	w.Debug("Generating code from buf...")
	err := runner.Load(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	_, err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	return nil
}
