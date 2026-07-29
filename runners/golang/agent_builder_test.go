package golang

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestGoDockerBuilderConfigurationUsesExactWorkspaceContext(t *testing.T) {
	workspace := t.TempDir()
	service := filepath.Join(workspace, "modules", "users", "services", "forge-edge")
	require.NoError(t, os.MkdirAll(service, 0o755))

	configuration, err := goDockerBuilderConfiguration(
		service,
		&resources.DockerImage{Name: "registry.example.com/forge-edge", Tag: "test"},
		io.Discard,
		DockerTemplating{ContextRoot: workspace, Workspace: true},
	)
	require.NoError(t, err)
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	require.NoError(t, err)
	require.Equal(t, resolvedWorkspace, configuration.Root)
	require.Equal(t, "modules/users/services/forge-edge/builder/Dockerfile", configuration.Dockerfile)
	require.Equal(t, "modules/users/services/forge-edge/builder/dockerignore", configuration.Ignorefile)
}

func TestGoDockerBuilderConfigurationRejectsServiceOutsideContext(t *testing.T) {
	workspace := t.TempDir()
	service := t.TempDir()

	_, err := goDockerBuilderConfiguration(
		service,
		&resources.DockerImage{Name: "registry.example.com/forge-edge", Tag: "test"},
		io.Discard,
		DockerTemplating{ContextRoot: workspace, Workspace: true},
	)
	require.ErrorContains(t, err, "outside Docker context root")
}
