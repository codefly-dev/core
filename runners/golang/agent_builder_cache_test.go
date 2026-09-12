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
	"github.com/codefly-dev/core/runners/rust"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:embed templates/builder
var cacheBuilderTemplates embed.FS

type recipeBuilder struct {
	*services.DefaultBuilder
	wrapper     *services.BuilderWrapper
	location    string
	language    string
	contextRoot string
}

func (b *recipeBuilder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	if b.language == "rust" {
		return rust.BuildRustDocker(ctx, b.wrapper, req, b.location, &builders.Dependencies{}, cacheBuilderTemplates, "", "")
	}
	return BuildGoDocker(ctx, b.wrapper, req, b.location, &builders.Dependencies{}, cacheBuilderTemplates, "", "", func(options *DockerTemplating) { options.ContextRoot = b.contextRoot })
}

func (*recipeBuilder) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
	return &builderv0.BuildCapabilitiesResponse{BuildxSelection: true}, nil
}

func TestImageRecipeOverGRPC(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, executable := range []string{"docker", "buildx"} {
		_, err := exec.LookPath(executable)
		require.Error(t, err)
	}
	for _, language := range []string{"go", "rust"} {
		for _, selection := range []string{"explicit", "cache"} {
			for _, scenario := range []string{"recipe", "empty-destination", "stale-destination", "symlink-destination", "missing-output", "relative-output", "custom-context"} {
				if language == "rust" && scenario == "custom-context" {
					continue
				}
				t.Run(language+"/"+selection+"/"+scenario, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					root := t.TempDir()
					location := filepath.Join(root, "service")
					output := filepath.Join(location, "builder")
					require.NoError(t, os.MkdirAll(output, 0o755))
					dockerfile := filepath.Join(output, "Dockerfile")
					require.NoError(t, os.WriteFile(dockerfile, []byte("original recipe"), 0o644))
					base := &services.Base{}
					require.NoError(t, base.HeadlessLoad(ctx, &basev0.ServiceIdentity{Name: "app", Module: "test", WorkspacePath: root, RelativeToWorkspace: "service"}))
					wrapper := &services.BuilderWrapper{Base: base}
					base.Builder = wrapper
					base.SetDockerImage(&resources.DockerImage{Name: "app", Tag: "test"})
					agent := &recipeBuilder{DefaultBuilder: services.NewDefaultBuilder(wrapper), wrapper: wrapper, location: location, language: language}
					dockerContext := &builderv0.DockerBuildContext{BuildxBuilder: "selected"}
					if selection == "cache" {
						dockerContext.BuildxBuilder = "cache-selected"
						dockerContext.Cache = &builderv0.BuildCacheOptions{Backend: "registry", Scope: "test/app", Exports: []string{"registry.example.com/cache"}}
					}
					request := &builderv0.BuildRequest{OutputDirectory: output, BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: dockerContext}}}
					expectedError := ""
					switch scenario {
					case "empty-destination", "stale-destination", "symlink-destination":
						request.OutputDirectory = filepath.Join(t.TempDir(), "recipes")
						if scenario == "stale-destination" {
							require.NoError(t, os.MkdirAll(request.OutputDirectory, 0o755))
							require.NoError(t, os.WriteFile(filepath.Join(request.OutputDirectory, "Dockerfile"), []byte("FROM stale-image\n"), 0o644))
						}
						if scenario == "symlink-destination" {
							require.NoError(t, os.MkdirAll(request.OutputDirectory, 0o755))
							require.NoError(t, os.Symlink(dockerfile, filepath.Join(request.OutputDirectory, "Dockerfile")))
						}
					case "missing-output":
						request.OutputDirectory = ""
						expectedError = "output_directory is required"
					case "relative-output":
						request.OutputDirectory = "builder"
						expectedError = "must be absolute"
					case "custom-context":
						agent.contextRoot = root
						expectedError = "custom Docker context root"
					}
					server := grpc.NewServer()
					builderv0.RegisterBuilderServer(server, agent)
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					go func() { _ = server.Serve(listener) }()
					t.Cleanup(server.Stop)
					connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, connection.Close()) })
					response, err := services.NewBuilderAgentClient(connection).Build(ctx, request)
					if expectedError != "" {
						if err != nil {
							require.ErrorContains(t, err, expectedError)
						} else {
							require.Equal(t, builderv0.BuildStatus_ERROR, response.GetState().GetState())
							require.Contains(t, response.GetState().GetMessage(), expectedError)
						}
						contents, readErr := os.ReadFile(dockerfile)
						require.NoError(t, readErr)
						require.Equal(t, "original recipe", string(contents))
						entries, readErr := os.ReadDir(output)
						require.NoError(t, readErr)
						require.Len(t, entries, 1)
						return
					}
					require.NoError(t, err)
					require.Equal(t, builderv0.BuildStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
					plan := response.GetResult().GetDockerBuildPlan()
					require.NotNil(t, plan)
					require.NoError(t, services.VerifyDockerBuildPlan(request.OutputDirectory, plan))
					fresh, readErr := os.ReadFile(filepath.Join(request.OutputDirectory, "Dockerfile"))
					require.NoError(t, readErr)
					require.Equal(t, "FROM scratch\nCOPY app /app\n", string(fresh))
					if request.OutputDirectory != output {
						original, readErr := os.ReadFile(dockerfile)
						require.NoError(t, readErr)
						require.Equal(t, "original recipe", string(original))
					}
					require.Len(t, plan.GetRecipes(), 1)
					require.Equal(t, "app:test", plan.GetRecipes()[0].GetImage())
					require.Equal(t, services.RecipeBuildPlatforms(), plan.GetRecipes()[0].GetPlatforms())
				})
			}
		}
	}
}
