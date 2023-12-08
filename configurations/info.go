package configurations

import (
	"embed"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/shared"
)

const InfoConfigurationName = "info.codefly.yaml"

type Info struct {
	Version string `yaml:"version"`
}

func Version() (string, error) {
	logger := shared.NewLogger().With("configurations.Version")
	conf, err := LoadFromFs[Info](shared.Embed(info))
	if err != nil {
		return "", logger.Wrapf(err, "cannot load configuration file <%s>", InfoConfigurationName)
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
