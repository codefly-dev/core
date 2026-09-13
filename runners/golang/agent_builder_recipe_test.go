package golang

import (
	"context"
	"embed"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
var recipeBuilderTemplates embed.FS

type recipeBuilder struct {
	*services.DefaultBuilder
	wrapper     *services.BuilderWrapper
	contextRoot string
	workspace   bool
}

func (b *recipeBuilder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	return BuildGoDocker(ctx, b.wrapper, req, &builders.Dependencies{}, recipeBuilderTemplates, "", "",
		func(options *DockerTemplating) {
			options.ContextRoot = b.contextRoot
			options.Workspace = b.workspace
		})
}

func (*recipeBuilder) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
	return &builderv0.BuildCapabilitiesResponse{BuildxSelection: true}, nil
}

// recipeAgent stands up the real gRPC Builder surface with Docker and Buildx
// absent from PATH, so nothing here can accidentally execute a build.
type recipeAgent struct {
	client  builderv0.BuilderClient
	service string
	output  string
	agent   *recipeBuilder
}

func newRecipeAgent(t *testing.T, ctx context.Context) *recipeAgent {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	for _, executable := range []string{"docker", "buildx"} {
		_, err := exec.LookPath(executable)
		require.Error(t, err)
	}

	root := t.TempDir()
	location := filepath.Join(root, "service")
	output := filepath.Join(location, "builder")
	require.NoError(t, os.MkdirAll(output, 0o755))

	base := &services.Base{}
	require.NoError(t, base.HeadlessLoad(ctx, &basev0.ServiceIdentity{
		Name: "app", Module: "test", WorkspacePath: root, RelativeToWorkspace: "service"}))
	wrapper := &services.BuilderWrapper{Base: base}
	base.Builder = wrapper
	base.SetDockerImage(&resources.DockerImage{Name: "app", Tag: "test"})

	agent := &recipeBuilder{DefaultBuilder: services.NewDefaultBuilder(wrapper), wrapper: wrapper}
	server := grpc.NewServer()
	builderv0.RegisterBuilderServer(server, agent)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	return &recipeAgent{client: builderv0.NewBuilderClient(connection), service: location, output: output, agent: agent}
}

func (a *recipeAgent) request(dockerContext *builderv0.DockerBuildContext) *builderv0.BuildRequest {
	return &builderv0.BuildRequest{
		OutputDirectory: a.output,
		BuildContext:    &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: dockerContext}},
	}
}

func selectedContext() *builderv0.DockerBuildContext {
	return &builderv0.DockerBuildContext{BuildxBuilder: "selected"}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestImageRecipeEmitsAVerifiablePlan(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)

	response, err := agent.client.Build(ctx, agent.request(selectedContext()))
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())

	plan := response.GetResult().GetDockerBuildPlan()
	require.NotNil(t, plan)
	require.NoError(t, services.VerifyDockerBuildPlan(agent.output, plan))
	require.Len(t, plan.GetRecipes(), 1)
	require.Equal(t, "app:test", plan.GetRecipes()[0].GetImage())
	require.Equal(t, services.RecipeBuildPlatforms(), plan.GetRecipes()[0].GetPlatforms())

	fresh, err := os.ReadFile(filepath.Join(agent.output, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\nCOPY app /app\n", string(fresh))

	// This template set emits no dockerignore, so the recipe must not claim one.
	require.Empty(t, plan.GetRecipes()[0].GetDockerignore())
}

// A stale recipe from an earlier build is replaced, not left in place.
func TestImageRecipeReplacesStaleDockerfile(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)
	require.NoError(t, os.WriteFile(filepath.Join(agent.output, "Dockerfile"), []byte("FROM stale-image\n"), 0o644))

	response, err := agent.client.Build(ctx, agent.request(selectedContext()))
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())

	fresh, err := os.ReadFile(filepath.Join(agent.output, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\nCOPY app /app\n", string(fresh))
}

// The recipe directory is committed, so it carries whatever the repository and
// the developer's OS put there. None of it is a build input — the build context
// is the service directory — so it must neither fail the build nor move the
// digest. A symlink here used to abort the build naming a file unrelated to the
// image; a stray sidecar file used to change the digest between macOS and CI.
func TestImageRecipeIsUnaffectedByUnrelatedContentInTheRecipeDirectory(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)

	clean, err := agent.client.Build(ctx, agent.request(selectedContext()))
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, clean.GetState().GetState(), clean.GetState().GetMessage())

	require.NoError(t, os.WriteFile(filepath.Join(agent.service, "shared.conf"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(agent.service, "shared.conf"), filepath.Join(agent.output, "shared.conf")))
	require.NoError(t, os.WriteFile(filepath.Join(agent.output, ".DS_Store"), []byte("mac"), 0o644))

	dirty, err := agent.client.Build(ctx, agent.request(selectedContext()))
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, dirty.GetState().GetState(), dirty.GetState().GetMessage())
	require.Equal(t,
		clean.GetResult().GetDockerBuildPlan().GetDigest(),
		dirty.GetResult().GetDockerBuildPlan().GetDigest())
	require.NoError(t, services.VerifyDockerBuildPlan(agent.output, dirty.GetResult().GetDockerBuildPlan()))
}

// Caller transport policy (which Buildx builder, which cache) is the executor's
// concern. It must not reach the recipe, or the same source would emit different
// durable artifacts depending on how the caller happened to invoke the build.
func TestImageRecipeIsIndependentOfCallerTransportPolicy(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)

	plain, err := agent.client.Build(ctx, agent.request(selectedContext()))
	require.NoError(t, err)

	cached := selectedContext()
	cached.BuildxBuilder = "cache-selected"
	cached.Cache = &builderv0.BuildCacheOptions{Backend: "registry", Scope: "test/app", Exports: []string{"registry.example.com/cache"}}
	selected, err := agent.client.Build(ctx, agent.request(cached))
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, selected.GetState().GetState(), selected.GetState().GetMessage())

	require.Equal(t,
		plain.GetResult().GetDockerBuildPlan().GetDigest(),
		selected.GetResult().GetDockerBuildPlan().GetDigest())
	// The agent executes nothing, so it acknowledges no executor selection.
	require.Empty(t, selected.GetBuildxBuilder())
	require.Empty(t, selected.GetCacheContractVersion())
}

func TestImageRecipeRejectsUnsupportedRequests(t *testing.T) {
	for name, scenario := range map[string]struct {
		mutate   func(*recipeAgent, *builderv0.BuildRequest)
		expected string
	}{
		"missing output_directory": {
			func(_ *recipeAgent, r *builderv0.BuildRequest) { r.OutputDirectory = "" },
			"output_directory is required",
		},
		"relative output_directory": {
			func(_ *recipeAgent, r *builderv0.BuildRequest) { r.OutputDirectory = "builder" },
			"must be absolute",
		},
		"custom context root": {
			func(a *recipeAgent, _ *builderv0.BuildRequest) { a.agent.contextRoot = a.service },
			"custom Docker context root",
		},
		// Workspace carries the same requirement as ContextRoot and agents set the
		// two together. Letting it through would emit a recipe whose context is the
		// service directory while the template copies workspace-relative paths —
		// failing later, inside the executor, with an opaque COPY error.
		"workspace context": {
			func(a *recipeAgent, _ *builderv0.BuildRequest) { a.agent.workspace = true },
			"workspace-root Docker contexts are not supported",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := testContext(t)
			agent := newRecipeAgent(t, ctx)
			require.NoError(t, os.WriteFile(filepath.Join(agent.output, "Dockerfile"), []byte("original recipe"), 0o644))

			request := agent.request(selectedContext())
			scenario.mutate(agent, request)

			response, err := agent.client.Build(ctx, request)
			if err != nil {
				require.ErrorContains(t, err, scenario.expected)
			} else {
				require.Equal(t, builderv0.BuildStatus_ERROR, response.GetState().GetState())
				require.Contains(t, response.GetState().GetMessage(), scenario.expected)
			}

			// A rejected request must not have touched the committed recipe.
			contents, readErr := os.ReadFile(filepath.Join(agent.output, "Dockerfile"))
			require.NoError(t, readErr)
			require.Equal(t, "original recipe", string(contents))
			entries, readErr := os.ReadDir(agent.output)
			require.NoError(t, readErr)
			require.Len(t, entries, 1)
		})
	}
}
