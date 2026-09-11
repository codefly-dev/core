package golang

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:embed templates/builder
var cacheBuilderTemplates embed.FS

type legacyGoCacheBuilder struct {
	*services.DefaultBuilder
	wrapper  *services.BuilderWrapper
	root     string
	location string
}

func (builder *legacyGoCacheBuilder) Build(ctx context.Context, request *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	return BuildGoDocker(ctx, builder.wrapper, request, builder.location, &builders.Dependencies{}, cacheBuilderTemplates, "", "", func(options *DockerTemplating) { options.ContextRoot = builder.root })
}

func (*legacyGoCacheBuilder) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
	return &builderv0.BuildCapabilitiesResponse{BuildxSelection: true}, nil
}

func TestCustomContextBuildUsesRequestedBuildxAndExportsCache(t *testing.T) {
	if os.Getenv("CODEFLY_TEST_REGISTRY_CACHE") != "1" {
		t.Skip("set CODEFLY_TEST_REGISTRY_CACHE=1 to build against a disposable registry")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		require.NoError(t, err, "%v: %s", args, stderr.String())
		return strings.TrimSpace(string(output))
	}
	cleanup := func(args ...string) {
		t.Helper()
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		output, err := exec.CommandContext(cleanupCtx, "docker", args...).CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	name := fmt.Sprintf("codefly-builder-%d", time.Now().UnixNano())
	run("network", "create", name)
	t.Cleanup(func() { cleanup("network", "rm", name) })
	registry := name + ".local"
	run("run", "-d", "--name", registry, "--network", name, "-p", "127.0.0.1::5000", "registry:2")
	t.Cleanup(func() { cleanup("rm", "-f", registry) })
	address := run("port", registry, "5000/tcp")
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + address + "/v2/")
		if err != nil {
			return false
		}
		closeErr := response.Body.Close()
		return response.StatusCode == http.StatusOK && closeErr == nil
	}, 20*time.Second, 100*time.Millisecond)
	config := filepath.Join(t.TempDir(), "buildkitd.toml")
	require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[registry.\"%s:5000\"]\n http = true\n", registry)), 0o600))
	run("buildx", "create", "--name", name, "--driver", "docker-container", "--driver-opt", "network="+name, "--buildkitd-config", config, "--bootstrap")
	t.Cleanup(func() { cleanup("buildx", "rm", name) })
	t.Setenv("BUILDX_BUILDER", "must-not-use-ambient-builder")
	root := t.TempDir()
	location := filepath.Join(root, "service")
	require.NoError(t, os.Mkdir(location, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app"), []byte("legacy cached image"), 0o644))
	base := &services.Base{}
	require.NoError(t, base.HeadlessLoad(ctx, &basev0.ServiceIdentity{Name: "app", Module: "test", WorkspacePath: root, RelativeToWorkspace: "service"}))
	wrapper := &services.BuilderWrapper{Base: base}
	base.Builder = wrapper
	image := &resources.DockerImage{Name: name, Tag: "test"}
	base.SetDockerImage(image)
	request := &builderv0.BuildRequest{
		OutputDirectory: filepath.Join(location, "builder"),
		BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{
			BuildxBuilder: name,
			Cache:         &builderv0.BuildCacheOptions{Backend: "registry", Scope: "test/app", Exports: []string{registry + ":5000/cache"}},
		}}},
	}
	server := grpc.NewServer()
	builderv0.RegisterBuilderServer(server, &legacyGoCacheBuilder{DefaultBuilder: services.NewDefaultBuilder(wrapper), wrapper: wrapper, root: root, location: location})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	response, err := services.NewBuilderAgentClient(connection).Build(ctx, request)
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState())
	t.Cleanup(func() { cleanup("image", "rm", image.FullName()) })
	require.Nil(t, response.GetResult().GetDockerBuildPlan())
	require.Equal(t, name, response.GetBuildxBuilder())
	require.Equal(t, "registry-v1", response.GetCacheContractVersion())
	require.Equal(t, "amd64", run("image", "inspect", "--format", "{{.Architecture}}", image.FullName()))
	tags, err := client.Get("http://" + address + "/v2/cache/tags/list")
	require.NoError(t, err)
	defer func() { require.NoError(t, tags.Body.Close()) }()
	require.Equal(t, http.StatusOK, tags.StatusCode)
	var manifest struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(tags.Body).Decode(&manifest))
	require.Len(t, manifest.Tags, 1)
	require.True(t, strings.HasPrefix(manifest.Tags[0], "codefly-"))
}
