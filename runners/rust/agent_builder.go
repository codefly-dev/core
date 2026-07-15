package rust

import (
	"context"
	"embed"
	"fmt"
	"strings"

	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerTemplating holds template parameters for Dockerfile generation.
// Mirrors golang.DockerTemplating with Rust toolchain fields.
type DockerTemplating struct {
	Components    []string
	Envs          []DockerEnv
	RustVersion   string
	AlpineVersion string
	SourceDir     string // e.g. "code" — the crate location
	ModuleRoot    string // e.g. "code" — where Cargo.toml lives
	BuildTarget   string // bin name to build (`cargo build --bin <target>`)
}

// DockerEnv is a key-value pair for Docker environment variables.
type DockerEnv struct {
	Key   string
	Value string
}

// BuildRustDocker generates templates and builds a Docker image for a Rust
// service. Mirrors golang.BuildGoDocker.
func BuildRustDocker(ctx context.Context, builder *services.BuilderWrapper,
	req *builderv0.BuildRequest, location string,
	requirements *builders.Dependencies, builderFS embed.FS,
	rustVersion, alpineVersion string, opts ...func(*DockerTemplating)) (*builderv0.BuildResponse, error) {

	w := wool.Get(ctx).In("rust.BuildRustDocker")

	dockerRequest, err := builder.DockerBuildRequest(ctx, req)
	if err != nil {
		return nil, w.Wrapf(err, "docker build request")
	}

	image := builder.DockerImage(dockerRequest)
	w.Debug("building docker image", wool.Field("image", image.FullName()))

	if !dockerhelpers.IsValidDockerImageName(image.Name) {
		return builder.BuildError(fmt.Errorf("invalid docker image name: %s", image.Name))
	}

	docker := DockerTemplating{
		Components:    requirements.All(),
		RustVersion:   rustVersion,
		AlpineVersion: alpineVersion,
	}
	for _, opt := range opts {
		opt(&docker)
	}

	_ = shared.DeleteFile(ctx, location+"/builder/Dockerfile")

	err = builder.Templates(ctx, docker, services.WithBuilder(builderFS))
	if err != nil {
		return builder.BuildError(err)
	}

	b, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:        location,
		Dockerfile:  "builder/Dockerfile",
		Ignorefile:  "builder/dockerignore",
		Destination: image,
		Output:      w,
	})
	if err != nil {
		return builder.BuildError(err)
	}
	_, err = b.Build(ctx)
	if err != nil {
		return builder.BuildError(err)
	}
	builder.WithDockerImages(image)
	return builder.BuildResponse()
}

// DeployRustKubernetes deploys a Rust service to Kubernetes. Identical in
// shape to golang.DeployGoKubernetes — the deployment path is language
// agnostic (env vars + config maps + secrets + kustomize).
func DeployRustKubernetes(ctx context.Context, builder *services.BuilderWrapper, req *builderv0.DeploymentRequest,
	envVars *resources.EnvironmentVariableManager, deploymentFS embed.FS) (*builderv0.DeploymentResponse, error) {
	return builder.DeployKustomize(ctx, req, services.KustomizeDeployment{
		EnvironmentVariables: envVars,
		Templates:            deploymentFS,
		Inputs:               services.ApplicationDeploymentInputs(),
	})
}

// SplitSourceDir splits a source directory like "code/crates/server" into a
// module root ("code") and a build target ("crates/server"). For "code"
// alone, returns ("code", "."). Mirrors golang.SplitSourceDir.
func SplitSourceDir(sourceDir string) (moduleRoot, buildTarget string) {
	parts := strings.SplitN(sourceDir, "/", 2)
	moduleRoot = parts[0]
	if len(parts) > 1 {
		buildTarget = parts[1]
	} else {
		buildTarget = "."
	}
	return
}
