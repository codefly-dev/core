//go:build proto_companion_required

package ciguard

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/stretchr/testify/require"
)

// A Dockerfile pin does not change the image consumers select. In particular,
// retaining an already published tag leaves cached consumers on its old buf,
// and publishing skips that tag. Check the executable in the selected image.
func TestSelectedProtoCompanionBufMatchesTheGate(t *testing.T) {
	ctx := t.Context()
	testutil.RequireProtoImage(t, ctx)
	img, err := proto.CompanionImage(ctx)
	require.NoError(t, err)
	output, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none",
		"--entrypoint", "buf", img.FullName(), "--version").CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Equal(t, makefileBufVersion(t), strings.TrimSpace(string(output)),
		"the selected companion %s runs a different buf than the gate; bump its image version and build/publish that version instead of reusing an old tag", img.FullName())
}
