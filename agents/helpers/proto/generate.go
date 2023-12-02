package proto

import (
	"context"
	"embed"
	"fmt"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/shared"
)

type Proto struct {
	Dir     string
	version string
}

func NewProto(dir string) (*Proto, error) {
	logger := shared.NewLogger("proto.NewProto")
	version, err := version()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get version")
	}
	return &Proto{
		Dir:     dir,
		version: version,
	}, nil
}

func version() (string, error) {
	logger := shared.NewLogger("configurations.Version")
	conf, err := configurations.LoadFromFs[configurations.Info](shared.Embed(info))
	if err != nil {
		return "", logger.Wrapf(err, "cannot load info for companion")
	}
	// check we have a valid semantic version
	v, err := semver.NewVersion(conf.Version)
	if err != nil {
		return "", logger.Wrapf(err, "cannot parse version <%s>", conf.Version)
	}
	return v.String(), nil
}

//go:embed info.codefly.yaml
var info embed.FS

func (g *Proto) Generate(ctx context.Context) error {
	logger := shared.AgentLogger(ctx)
	image := fmt.Sprintf("codeflydev/companion:%s", g.version)
	volume := fmt.Sprintf("%s:/workspace", g.Dir)
	runner := runners.Runner{Dir: g.Dir, Bin: "docker", Args: []string{"run", "--rm", "-v", volume, image, "buf", "generate"}, AgentLogger: logger, ServiceLogger: shared.ServiceLogger(ctx)}
	logger.Debugf("Generating code from buf...")
	err := runner.Init(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot generate code from buf")
	}
	_, err = runner.Run(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot generate code from buf")
	}
	return nil
}
