package configurations

import (
	"context"
	"embed"

	"github.com/codefly-dev/core/wool"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/shared"
)

const InfoConfigurationName = "info.codefly.yaml"

type Info struct {
	Version string `yaml:"version"`
}

func Version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("Version")
	conf, err := LoadFromFs[Info](shared.Embed(info))
	if err != nil {
		return "", w.Wrapf(err, "cannot load info from filesystem")
	}
	// check we have a valid semantic version
	v, err := semver.NewVersion(conf.Version)
	if err != nil {
		return "", w.Wrapf(err, "cannot parse version: <%s>", conf.Version)
	}
	return v.String(), nil
}

//go:embed info.codefly.yaml
var info embed.FS
