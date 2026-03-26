package golang

import (
	"context"
	"embed"
	"fmt"

	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerTemplating holds template parameters for Dockerfile generation.
type DockerTemplating struct {
	Components    []string
	Envs          []DockerEnv
	GoVersion     string
	AlpineVersion string
}

// DockerEnv is a key-value pair for Docker environment variables.
type DockerEnv struct {
	Key   string
	Value string
}

// BuildGoDocker builds a Docker image for a Go service.
// It handles the full flow: extract build context, generate Dockerfile from
// templates, build image, and register result.
func BuildGoDocker(ctx context.Context, builder *services.BuilderWrapper,
	location string, requirements *builders.Dependencies, builderFS embed.FS,
	goVersion, alpineVersion string) (*builderv0.BuildResponse, error) {

	w := wool.Get(ctx).In("golang.BuildGoDocker")

	docker := DockerTemplating{
		Components:    requirements.All(),
		GoVersion:     goVersion,
		AlpineVersion: alpineVersion,
	}

	_ = shared.DeleteFile(ctx, location+"/builder/Dockerfile")

	err := builder.Templates(ctx, docker, services.WithBuilder(builderFS))
	if err != nil {
		return builder.BuildError(err)
	}

	w.Debug("templates generated for docker build")

	return nil, nil
}

// BuildDockerImage builds a Docker image from the prepared Dockerfile.
// Call after templates have been generated (e.g. via BuildGoDocker or manually).
func BuildDockerImage(ctx context.Context, builder *services.BuilderWrapper,
	req *builderv0.BuildRequest, location string) (*builderv0.BuildResponse, error) {

	w := wool.Get(ctx).In("golang.BuildDockerImage")

	dockerRequest, err := builder.DockerBuildRequest(ctx, req)
	if err != nil {
		return nil, w.Wrapf(err, "docker build request")
	}

	image := builder.DockerImage(dockerRequest)
	w.Debug("building docker image", wool.Field("image", image.FullName()))

	if !dockerhelpers.IsValidDockerImageName(image.Name) {
		return builder.BuildError(fmt.Errorf("invalid docker image name: %s", image.Name))
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

// DeployGoKubernetes deploys a Go service to Kubernetes.
// Handles environment variable setup, config maps, secrets, and kustomize generation.
func DeployGoKubernetes(ctx context.Context, builder *services.BuilderWrapper, req *builderv0.DeploymentRequest,
	envVars *resources.EnvironmentVariableManager, deploymentFS embed.FS) (*builderv0.DeploymentResponse, error) {

	w := wool.Get(ctx).In("golang.DeployGoKubernetes")

	builder.LogDeployRequest(req, w.Debug)
	envVars.SetRunning()

	k, err := builder.KubernetesDeploymentRequest(ctx, req)
	if err != nil {
		return builder.DeployError(err)
	}

	err = envVars.AddEndpoints(ctx,
		resources.LocalizeNetworkMapping(req.NetworkMappings, "localhost"),
		resources.NewContainerNetworkAccess())
	if err != nil {
		return builder.DeployError(err)
	}
	err = envVars.AddEndpoints(ctx, req.DependenciesNetworkMappings, resources.NewContainerNetworkAccess())
	if err != nil {
		return builder.DeployError(err)
	}
	err = envVars.AddConfigurations(ctx, req.Configuration)
	if err != nil {
		return builder.DeployError(err)
	}
	err = envVars.AddConfigurations(ctx, req.DependenciesConfigurations...)
	if err != nil {
		return builder.DeployError(err)
	}

	confs, err := envVars.Configurations()
	if err != nil {
		return builder.DeployError(err)
	}
	cm, err := services.EnvsAsConfigMapData(confs...)
	if err != nil {
		return builder.DeployError(err)
	}
	secrets, err := services.EnvsAsSecretData(envVars.Secrets()...)
	if err != nil {
		return builder.DeployError(err)
	}

	params := services.DeploymentParameters{
		ConfigMap: cm,
		SecretMap: secrets,
	}
	err = builder.KustomizeDeploy(ctx, req.Environment, k, deploymentFS, params)
	if err != nil {
		return builder.DeployError(err)
	}
	return builder.DeployResponse()
}
