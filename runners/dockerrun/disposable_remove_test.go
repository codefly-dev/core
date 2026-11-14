package dockerrun

import (
	"errors"
	"fmt"
	"testing"

	dockererrors "github.com/docker/docker/errdefs"
	"github.com/stretchr/testify/require"
)

func TestRemovalInProgressRequiresSpecificConflict(t *testing.T) {
	const id = "abc123"
	message := fmt.Sprintf("removal of container %s is already in progress", id)
	require.True(t, removalInProgress(dockererrors.Conflict(errors.New("Error response from daemon: "+message)), id))
	require.False(t, removalInProgress(errors.New(message), id))
	require.False(t, removalInProgress(dockererrors.Conflict(errors.New("other conflict")), id))
	require.False(t, removalInProgress(dockererrors.Conflict(errors.New("removal of container another is already in progress")), id))
}
