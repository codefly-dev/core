package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/toolbox/docker"
)

func TestDocker_Identity(t *testing.T) {
	srv := docker.New("0.0.1")
	resp, err := srv.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "docker", resp.Name)
	require.Equal(t, []string{"docker"}, resp.CanonicalFor,
		"docker toolbox owns the `docker` binary in the canonical-routing layer")
	require.NotEmpty(t, resp.SandboxSummary)
}

func TestDocker_ListTools_Stable(t *testing.T) {
	srv := docker.New("0.0.1")
	resp, err := srv.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{
		"docker.list_containers",
		"docker.list_images",
		"docker.inspect_container",
	}, names, "if the surface changes, pin it here")
}

func TestDocker_UnknownTool_ActionableError(t *testing.T) {
	srv := docker.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "docker.bogus"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "docker.bogus")
	require.Contains(t, resp.Error, "ListTools")
}

func TestDocker_InspectContainer_RequiresID(t *testing.T) {
	srv := docker.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "docker.inspect_container",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "id is required",
		"missing id must be surfaced cleanly, not a daemon error")
}

// Daemon-touching tests — only run when Docker is actually up.
// The codebase rule (feedback_no_t_skip) says: fail loud if infra
// missing instead of silent skip. Build-tag this file appropriately
// when we add CI matrix that doesn't have Docker.

func TestDocker_ListContainers_HitsDaemon(t *testing.T) {
	if !base.DockerEngineRunning(context.Background()) {
		t.Fatal("docker daemon not running; required for this integration test (or run with -tags skip_infra to exclude)")
	}
	srv := docker.New("0.0.1")
	defer srv.Close()

	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "docker.list_containers",
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	_, ok := out["containers"].([]any)
	require.True(t, ok, "containers field must be a list (possibly empty)")
}

func TestDocker_ListImages_HitsDaemon(t *testing.T) {
	if !base.DockerEngineRunning(context.Background()) {
		t.Fatal("docker daemon not running; required for this integration test (or run with -tags skip_infra to exclude)")
	}
	srv := docker.New("0.0.1")
	defer srv.Close()

	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "docker.list_images",
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	images, ok := out["images"].([]any)
	require.True(t, ok)
	// On a developer box there's almost always at least one image
	// (alpine, golang, the codefly companion images we just built).
	require.NotEmpty(t, images, "developer Docker daemon should have at least one image")
}
