// Package python holds the Python companion: its recipe lives alongside its
// Dockerfile in this directory, and the version the DAP path pulls is the one
// its caller's companion index names.
package python

import (
	"context"

	companionindex "github.com/codefly-dev/core/companions/index"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// CompanionImage returns the Python companion image the caller's companion
// index names (see companionindex): core compiles no companion version in.
func CompanionImage(ctx context.Context) (*resources.DockerImage, error) {
	w := wool.Get(ctx).In("python.CompanionImage")
	v, err := companionindex.Version("python")
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	img := resources.PublishedImage("python", v)
	return &img, nil
}
