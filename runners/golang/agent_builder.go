package golang

import (
	"context"
	"embed"
	"fmt"
	"strings"

	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerTemplating holds template parameters for Dockerfile generation.
type DockerTemplating struct {
	Components    []string
	Envs          []DockerEnv
	GoVersion     string
	AlpineVersion string
	// WithCGO selects a native toolchain build for services that bind C
	// libraries. Agent templates must keep the default false path static and
	// install only the build dependencies required by the true path.
	WithCGO     bool
	SourceDir   string // e.g. "code/cmd/server" — the Go main package location
	ModuleRoot  string // e.g. "code" — where go.mod lives
	BuildTarget string // e.g. "./cmd/server" — package to build (relative to ModuleRoot)
	ContextRoot string // non-empty custom contexts are rejected before preparation
	Workspace   bool   // template hint for workspace-aware source copying
}

// DockerEnv is a key-value pair for Docker environment variables.
type DockerEnv struct {
	Key   string
	Value string
}

// BuildGoDocker emits a Docker build recipe for a Go service.
func BuildGoDocker(ctx context.Context, builder *services.BuilderWrapper,
	req *builderv0.BuildRequest, _ string,
	requirements *builders.Dependencies, builderFS embed.FS,
	goVersion, alpineVersion string, opts ...func(*DockerTemplating)) (*builderv0.BuildResponse, error) {

	w := wool.Get(ctx).In("golang.BuildGoDocker")

	if !services.BuildPlanRequested(req) {
		return builder.BuildError(fmt.Errorf("BuildRequest.output_directory is required for image recipes"))
	}

	dockerRequest, err := builder.DockerBuildRequest(ctx, req)
	if err != nil {
		return nil, w.Wrapf(err, "docker build request")
	}

	image := builder.DockerImage(dockerRequest)
	w.Debug("preparing docker image recipe", wool.Field("image", image.FullName()))

	if !dockerhelpers.IsValidDockerImageName(image.Name) {
		return builder.BuildError(fmt.Errorf("invalid docker image name: %s", image.Name))
	}

	docker := DockerTemplating{
		Components:    requirements.All(),
		GoVersion:     goVersion,
		AlpineVersion: alpineVersion,
	}
	for _, opt := range opts {
		opt(&docker)
	}

	if docker.ContextRoot != "" {
		return builder.BuildError(fmt.Errorf("custom Docker context root %q is not supported by image recipes", docker.ContextRoot))
	}

	err = builder.Templates(ctx, docker, services.WithBuilder(builderFS).WithDestination("%s", req.GetOutputDirectory()))
	if err != nil {
		return builder.BuildError(err)
	}

	return builder.SingleImageBuildResponse(req, image.FullName())
}

// DeployGoKubernetes deploys a Go service to Kubernetes.
// Handles environment variable setup, config maps, secrets, and kustomize generation.
// Pass options such as services.WithPodOverlay to opt into pod-level
// customization (serviceAccountName, an annotated ServiceAccount, pod labels)
// without re-inlining the DeployKustomize wiring.
func DeployGoKubernetes(ctx context.Context, builder *services.BuilderWrapper, req *builderv0.DeploymentRequest,
	envVars *resources.EnvironmentVariableManager, deploymentFS embed.FS,
	opts ...services.KustomizeDeploymentOption) (*builderv0.DeploymentResponse, error) {
	deployment := services.KustomizeDeployment{
		EnvironmentVariables: envVars,
		Templates:            deploymentFS,
		Inputs:               services.ApplicationDeploymentInputs(),
	}
	for _, opt := range opts {
		opt(&deployment)
	}
	return builder.DeployKustomize(ctx, req, deployment)
}

// SplitSourceDir splits a source directory like "code/cmd/server" into
// a module root ("code") and a build target ("./cmd/server").
// For "code" alone, returns ("code", ".").
func SplitSourceDir(sourceDir string) (moduleRoot, buildTarget string) {
	// Find the first path component — that's where go.mod lives.
	parts := strings.SplitN(sourceDir, "/", 2)
	moduleRoot = parts[0]
	if len(parts) > 1 {
		buildTarget = "./" + parts[1]
	} else {
		buildTarget = "."
	}
	return
}
