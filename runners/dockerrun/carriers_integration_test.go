//go:build !skip_infra

package dockerrun_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// A container that runs as a non-root user reads the values Codefly delivers
// to it by file, public and secret, at start and at exec; the files are its
// user's alone.
func TestANonRootContainerReadsItsFileDeliveredValues(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	name := fmt.Sprintf("test-carriers-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), name)
	require.NoError(t, err)
	defer func() { require.NoError(t, env.Shutdown(ctx)) }()
	env.WithUser("nobody")
	env.WithPause()

	public := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__RUNNABLE_BINDINGS__DESCRIPTOR_SET__AB", strings.Repeat("p", resources.FileCarrierThreshold+1))
	public.File = true
	secret := resources.Env("CODEFLY__WORKSPACE_SECRET_CONFIGURATION__VAULT__BUNDLE", strings.Repeat("s", resources.FileCarrierThreshold+1))
	secret.File, secret.Secret = true, true
	env.WithEnvironmentVariables(ctx, public, secret)
	require.NoError(t, env.Init(ctx))

	run := func(envs []*resources.EnvironmentVariable, script string) string {
		proc, err := env.NewProcess("sh", "-c", script)
		require.NoError(t, err)
		proc.WithEnvironmentVariables(ctx, envs...)
		output := shared.NewSliceWriter()
		proc.WithOutput(output)
		require.NoError(t, proc.Run(ctx))
		return strings.Join(output.Snapshot(), "\n")
	}
	check := `id -u; for v in "$CODEFLY__FILE__WORKSPACE_CONFIGURATION__RUNNABLE_BINDINGS__DESCRIPTOR_SET__AB" "$CODEFLY__FILE__WORKSPACE_SECRET_CONFIGURATION__VAULT__BUNDLE"; do stat -c '%u %a' "$v"; wc -c < "$v"; done`
	lines := strings.Fields(run(nil, check))
	require.Equal(t, []string{"65534",
		"65534", "400", fmt.Sprint(resources.FileCarrierThreshold + 1),
		"65534", "400", fmt.Sprint(resources.FileCarrierThreshold + 1)}, lines)

	// A value delivered by file at exec reaches the running container too.
	late := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__LATE__VALUE", strings.Repeat("l", resources.FileCarrierThreshold+1))
	late.File = true
	lines = strings.Fields(run([]*resources.EnvironmentVariable{late}, `id -u; wc -c < "$CODEFLY__FILE__WORKSPACE_CONFIGURATION__LATE__VALUE"`))
	require.Equal(t, []string{"65534", fmt.Sprint(resources.FileCarrierThreshold + 1)}, lines)
}
