// Package python derives the Python companion image from the buildable
// manifest that lives alongside its Dockerfile in this directory, so the
// tag the DAP path pulls at runtime is the same one
// `codefly companion publish` builds.
package python

import (
	"context"
	"embed"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// Info holds companion version metadata.
type Info struct {
	Version string `yaml:"version"`
}

func version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("python.version")

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

// CompanionImage returns the Docker image for the Python companion (which
// carries debugpy for the DAP path).
func CompanionImage(ctx context.Context) (*resources.DockerImage, error) {
	w := wool.Get(ctx).In("python.CompanionImage")
	v, err := version(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	return &resources.DockerImage{Name: "codeflydev/python", Tag: v}, nil
}

//go:embed info.codefly.yaml
var infoFS embed.FS
