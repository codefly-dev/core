// Package companions declares the Docker images agents pull at runtime and the
// build inputs that produce them, so tooling works from the exact embedded set
// instead of inferring tags from directory names and manifests. Every tag here
// is derived from a companion's info.codefly.yaml, the same source
// `codefly companion publish` builds and pushes.
package companions

import (
	"context"
	"embed"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

//go:embed codefly/info.codefly.yaml execution/info.codefly.yaml go/info.codefly.yaml node/info.codefly.yaml proto/info.codefly.yaml python/info.codefly.yaml
var infoFS embed.FS

// Embedded returns every Docker image agents pull at runtime, each tag
// derived from the companion's info.codefly.yaml.
//
// This is the only place a companion is addressed through a registry. The
// build inputs BuildSpecs hands the builder name the image and its version
// and nothing more — where a companion is published is the builder's to
// resolve, and this pull-side qualification is the seam that follows it there.
func Embedded(ctx context.Context) ([]resources.DockerImage, error) {
	w := wool.Get(ctx).In("companions.Embedded")

	specs, err := BuildSpecs()
	if err != nil {
		return nil, w.Wrapf(err, "cannot derive companion images")
	}
	images := make([]resources.DockerImage, 0, len(specs))
	for _, spec := range specs {
		images = append(images, resources.PublishedImage(spec.Name, spec.Version))
	}
	return images, nil
}

func manifestVersion(dir string) (string, error) {
	content, err := fs.ReadFile(infoFS, dir+"/info.codefly.yaml")
	if err != nil {
		return "", err
	}
	var info struct {
		Version string `yaml:"version"`
	}
	if err = yaml.Unmarshal(content, &info); err != nil {
		return "", err
	}
	v, err := semver.NewVersion(info.Version)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
