package proto

import (
	"context"

	companionindex "github.com/codefly-dev/core/companions/index"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// CompanionImage returns the proto companion image the caller's companion
// index names (see companionindex): core compiles no companion version in.
func CompanionImage(ctx context.Context) (*resources.DockerImage, error) {
	w := wool.Get(ctx).In("proto.CompanionImage")
	v, err := companionindex.Version("proto")
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	img := resources.PublishedImage("proto", v)
	return &img, nil
}
