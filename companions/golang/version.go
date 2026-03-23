package golang

import (
	"context"
	"embed"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/wool"
)

// Info holds companion version metadata.
type Info struct {
	Version string `yaml:"version"`
}

func version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("golang.version")

	content, err := fs.ReadFile(infoFS, "info.codefly.yaml")
	if err != nil {
		return "", w.Wrapf(err, "cannot read file")
	}
	var info Info
	if err = yaml.Unmarshal(content, &info); err != nil {
		return "", w.Wrapf(err, "cannot unmarshal file")
	}
	v, err := semver.NewVersion(info.Version)
	if err != nil {
		return "", w.Wrapf(err, "cannot parse version <%s>", info.Version)
	}
	return v.String(), nil
}

// CompanionImage returns the Docker image for the Go companion.
func CompanionImage(ctx context.Context) (*resources.DockerImage, error) {
	w := wool.Get(ctx).In("golang.CompanionImage")
	v, err := version(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	return &resources.DockerImage{Name: "codeflydev/go", Tag: v}, nil
}

//go:embed info.codefly.yaml
var infoFS embed.FS
