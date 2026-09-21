//go:build sandbox_e2e

package manager

import (
	"os"
	"testing"

	"github.com/codefly-dev/core/artifactexecution"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/stretchr/testify/require"
)

func TestLoadArtifactSandbox(t *testing.T) {
	// Keep the real UDS below the platform's sun_path limit.
	tmp, err := os.MkdirTemp("/tmp", "executor-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	path, digest := buildAcquiredExecutor(t, "sandbox")
	request := preparedExecutor(t, digest, artifactexecution.BuilderBuild)
	staging := t.TempDir()
	sb, err := sandbox.New()
	require.NoError(t, err)
	sb.WithWritePaths(sandboxPathAliases(staging)...).WithNetwork(sandbox.NetworkDeny)
	conn, err := LoadArtifact(t.Context(), path, request, WithSandbox(sb), WithoutPrincipal(), WithUDS(), WithWorkDir(staging))
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	response, err := builderv0.NewBuilderClient(conn.GRPCConn()).Build(t.Context(), &builderv0.BuildRequest{Execution: request, OutputDirectory: staging})
	require.NoError(t, err)
	require.NoError(t, artifactexecution.CheckReceipt(request, response.Execution))
	conn.Close()
	require.NoDirExists(t, conn.runtimeDir)
	require.NoDirExists(t, conn.executableDir)
	require.FileExists(t, path)
}
