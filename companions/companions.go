// Package companions enumerates the Docker images agents pull at runtime and
// declares the build inputs that produce them. Which version of each image a
// run uses is data its caller supplies (the companion index, see
// companionindex); a companion's info.codefly.yaml is its recipe, the version
// `codefly companion publish` builds and pushes from a checkout.
package companions

import (
	"context"

	golang "github.com/codefly-dev/core/companions/go"
	companionindex "github.com/codefly-dev/core/companions/index"
	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/python"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// derived maps a companion directory to the image agents pull for companions
// that have no dedicated derivation package. The tag comes from the companion
// index.
var derived = []struct {
	dir  string
	name string
}{
	{dir: "node", name: "node"},
	{dir: "codefly", name: "codefly"},
}

// Images returns every Docker image agents pull at runtime, each tag the one
// the companion index names.
func Images(ctx context.Context) ([]resources.DockerImage, error) {
	w := wool.Get(ctx).In("companions.Images")

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
		v, err := companionindex.Version(c.dir)
		if err != nil {
			return nil, w.Wrapf(err, "cannot derive <%s> companion image", c.dir)
		}
		images = append(images, resources.PublishedImage(c.name, v))
	}

	return images, nil
}
