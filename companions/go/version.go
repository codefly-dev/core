// Package golang holds the Go companion: its recipe lives alongside its
// Dockerfile in this directory, and the version a run pulls is the one its
// caller's companion index names. The package is named golang because "go"
// is a reserved word.
package golang

import (
	"context"

	companionindex "github.com/codefly-dev/core/companions/index"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// CompanionImage returns the Go companion image the caller's companion
// index names (see companionindex): core compiles no companion version in.
func CompanionImage(ctx context.Context) (*resources.DockerImage, error) {
	w := wool.Get(ctx).In("golang.CompanionImage")
	v, err := companionindex.Version("go")
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	img := resources.PublishedImage("go", v)
	return &img, nil
}
