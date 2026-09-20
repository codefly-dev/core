//go:build proto_companion_required

package ciguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
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

// The formatting pass is part of generation (companions/proto.FormatGoOutputs)
// and it runs inside the companion, so the image must carry goimports. A
// Dockerfile that installs it in the builder stage and forgets the COPY, or a
// consumer left on a cached older tag, both fail the same way: buf succeeds,
// then the generate step errors on a missing binary. Check the selected image.
func TestSelectedProtoCompanionShipsGoimports(t *testing.T) {
	ctx := t.Context()
	testutil.RequireProtoImage(t, ctx)
	img, err := proto.CompanionImage(ctx)
	require.NoError(t, err)
	output, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none",
		"--entrypoint", "sh", img.FullName(), "-c", "command -v goimports").CombinedOutput()
	require.NoError(t, err, "the selected companion %s has no goimports on PATH; generation would format nothing and every consumer's tree would drift by import shape: %s", img.FullName(), output)
	require.Contains(t, string(output), "goimports")
}

// goimports names a package through `go list` in the consumer's module and
// refuses a module whose go.mod requires a newer Go than the image carries,
// then formats syntax only. Ask the selected image which Go it runs and hold
// it to what this repository's own module requires, since every consumer's
// go.mod moves with it.
func TestSelectedProtoCompanionGoSatisfiesTheEcosystem(t *testing.T) {
	ctx := t.Context()
	testutil.RequireProtoImage(t, ctx)
	img, err := proto.CompanionImage(ctx)
	require.NoError(t, err)
	output, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none",
		"--entrypoint", "go", img.FullName(), "env", "GOVERSION").CombinedOutput()
	require.NoError(t, err, "%s", output)
	imageGo, err := semver.NewVersion(strings.TrimPrefix(strings.TrimSpace(string(output)), "go"))
	require.NoError(t, err, "cannot parse the image's GOVERSION %q", output)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	require.NoError(t, err)
	parsed, err := modfile.Parse("go.mod", raw, nil)
	require.NoError(t, err)
	require.NotNil(t, parsed.Go, "go.mod has no go directive")
	required, err := semver.NewVersion(parsed.Go.Version)
	require.NoError(t, err)
	require.Falsef(t, imageGo.LessThan(required),
		"the selected companion %s runs go %s but this repository's go.mod requires go %s; goimports in that image cannot load consumer modules and silently formats syntax only", img.FullName(), imageGo, required)
}
