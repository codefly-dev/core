// Package companions enumerates the Docker images agents pull at runtime and
// declares the build inputs that produce them, so tooling works from the exact
// embedded set instead of inferring tags from directory names and manifests.
// Every tag here is derived from a companion's info.codefly.yaml, the same
// source `codefly companion publish` builds and pushes.
package companions

import (
	"context"
	"embed"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	golang "github.com/codefly-dev/core/companions/go"
	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/python"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

//go:embed codefly/info.codefly.yaml execution/info.codefly.yaml go/info.codefly.yaml node/info.codefly.yaml proto/info.codefly.yaml python/info.codefly.yaml
var infoFS embed.FS

// derived maps a companion directory to the image agents pull for companions
// that have no dedicated derivation package. The tag comes from the
// directory's info.codefly.yaml.
var derived = []struct {
	dir  string
	name string
}{
	{dir: "node", name: "node"},
	{dir: "execution", name: "execution"},
	{dir: "codefly", name: "codefly"},
}

// Embedded returns every Docker image agents pull at runtime, each tag
// derived from the companion's info.codefly.yaml.
func Embedded(ctx context.Context) ([]resources.DockerImage, error) {
	w := wool.Get(ctx).In("companions.Embedded")

	var images []resources.DockerImage
	for _, from := range []func(context.Context) (*resources.DockerImage, error){
		proto.CompanionImage,
		golang.CompanionImage,
		python.CompanionImage,
	} {
		img, err := from(ctx)
		if err != nil {
			return nil, w.Wrapf(err, "cannot derive companion image")
		}
		images = append(images, *img)
	}

	for _, c := range derived {
		v, err := manifestVersion(c.dir)
		if err != nil {
			return nil, w.Wrapf(err, "cannot derive <%s> companion image", c.dir)
		}
		images = append(images, resources.PublishedImage(c.name, v))
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
