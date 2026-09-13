package rust

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

// A two-file template set — Dockerfile and dockerignore — so the runner is
// exercised with a recipe that emits more than one file, which is where a guard
// scoped to a single filename leaks.
//
//go:embed templates/builder
var recipeBuilderTemplates embed.FS

type recipeBuilder struct {
	*services.DefaultBuilder
	wrapper *services.BuilderWrapper
}

func (b *recipeBuilder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	return BuildRustDocker(ctx, b.wrapper, req, &builders.Dependencies{}, recipeBuilderTemplates, "", "")
}

type recipeAgent struct {
	client  builderv0.BuilderClient
	service string
	output  string
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

	server := grpc.NewServer()
	builderv0.RegisterBuilderServer(server, &recipeBuilder{DefaultBuilder: services.NewDefaultBuilder(wrapper), wrapper: wrapper})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	return &recipeAgent{client: builderv0.NewBuilderClient(connection), service: location, output: output}
}

func (a *recipeAgent) request() *builderv0.BuildRequest {
	return &builderv0.BuildRequest{
		OutputDirectory: a.output,
		BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{
			DockerBuildContext: &builderv0.DockerBuildContext{BuildxBuilder: "selected"}}},
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestImageRecipeEmitsEveryTemplateFile(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)

	response, err := agent.client.Build(ctx, agent.request())
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())

	plan := response.GetResult().GetDockerBuildPlan()
	require.NoError(t, services.VerifyDockerBuildPlan(agent.output, plan))
	require.Equal(t, "dockerignore", plan.GetRecipes()[0].GetDockerignore())

	inventory := make([]string, 0, len(plan.GetFiles()))
	for _, file := range plan.GetFiles() {
		inventory = append(inventory, file.GetPath())
	}
	require.Equal(t, []string{"Dockerfile", "dockerignore"}, inventory)
}

// The renderer follows symlinks on write, so any emitted path that is a symlink
// gets written *through*, truncating a file outside the caller-owned directory.
// A guard scoped to "Dockerfile" left every other file in the template set
// exposed; the recipe directory is committed, so a symlinked dockerignore is
// ordinary repository content rather than an attack precondition.
func TestImageRecipeNeverWritesThroughASymlinkedDestination(t *testing.T) {
	ctx := testContext(t)
	agent := newRecipeAgent(t, ctx)

	targets := map[string]string{"Dockerfile": "dockerfile target", "dockerignore": "ignore target"}
	for name, content := range targets {
		outside := filepath.Join(agent.service, name+".outside")
		require.NoError(t, os.WriteFile(outside, []byte(content), 0o644))
		require.NoError(t, os.Symlink(outside, filepath.Join(agent.output, name)))
	}

	response, err := agent.client.Build(ctx, agent.request())
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
	require.NoError(t, services.VerifyDockerBuildPlan(agent.output, response.GetResult().GetDockerBuildPlan()))

	for name, content := range targets {
		got, readErr := os.ReadFile(filepath.Join(agent.service, name+".outside"))
		require.NoError(t, readErr)
		require.Equal(t, content, string(got), "%s target was written through the symlink", name)
	}

	fresh, err := os.ReadFile(filepath.Join(agent.output, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\nCOPY app /app\n", string(fresh))
}

func TestImageRecipeRequiresAnAbsoluteDestination(t *testing.T) {
	for name, scenario := range map[string]struct{ directory, expected string }{
		"missing":  {"", "output_directory is required"},
		"relative": {"builder", "must be absolute"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := testContext(t)
			agent := newRecipeAgent(t, ctx)
			require.NoError(t, os.WriteFile(filepath.Join(agent.output, "Dockerfile"), []byte("original recipe"), 0o644))

			request := agent.request()
			request.OutputDirectory = scenario.directory
			response, err := agent.client.Build(ctx, request)
			if err != nil {
				require.ErrorContains(t, err, scenario.expected)
			} else {
				require.Equal(t, builderv0.BuildStatus_ERROR, response.GetState().GetState())
				require.Contains(t, response.GetState().GetMessage(), scenario.expected)
			}

			contents, readErr := os.ReadFile(filepath.Join(agent.output, "Dockerfile"))
			require.NoError(t, readErr)
			require.Equal(t, "original recipe", string(contents))
		})
	}
}
