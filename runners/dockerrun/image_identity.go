package dockerrun

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/opencontainers/go-digest"
)

// PinImage freezes this environment's selected image to the local daemon's
// content-addressed image ID before Init. The ID identifies the image config
// and its root filesystem, not a registry manifest; never append it to a
// repository name as if it were a RepoDigest. Both container creation and the
// reuse fingerprint use this ID, so a moving tag cannot change the bytes while
// a consumer records the earlier identity. Repeated calls retain the same pin.
func (docker *DockerEnvironment) PinImage(ctx context.Context) (string, error) {
	if docker.instance != nil {
		return "", fmt.Errorf("cannot pin an image after container initialization")
	}
	inspected, err := docker.client.ImageInspect(ctx, docker.image.FullName())
	if err != nil {
		return "", fmt.Errorf("inspect runtime image identity: %w", err)
	}
	id, err := digest.Parse(inspected.ID)
	if err != nil || id.Algorithm() != digest.SHA256 || inspected.ID != strings.ToLower(inspected.ID) {
		return "", fmt.Errorf("runtime image has no valid sha256 content identity")
	}
	docker.image = &resources.DockerImage{Name: inspected.ID}
	return inspected.ID, nil
}
