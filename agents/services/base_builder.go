package services

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
	"github.com/codefly-dev/core/shared"
)

type BuilderWrapper struct {
	*Base

	// Builder only has one configuration
	Configuration *basev0.Configuration
}

func (s *BuilderWrapper) LoadResponse(gettingStarted string) (*builderv0.LoadResponse, error) {
	if !s.loaded {
		return s.LoadError(fmt.Errorf("not loaded"))
	}
	for _, e := range s.Endpoints {
		e.Application = s.Identity.Application
		e.Service = s.Identity.Name
	}
	return &builderv0.LoadResponse{
		Version:        s.Version(),
		Endpoints:      s.Endpoints,
		GettingStarted: gettingStarted,
		State:          &builderv0.LoadStatus{State: builderv0.LoadStatus_READY},
	}, nil
}

func (s *BuilderWrapper) LoadError(err error) (*builderv0.LoadResponse, error) {
	return &builderv0.LoadResponse{
		State: &builderv0.LoadStatus{State: builderv0.LoadStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *BuilderWrapper) InitResponse() (*builderv0.InitResponse, error) {
	if !s.loaded {
		return s.InitError(fmt.Errorf("not loaded"))
	}
	return &builderv0.InitResponse{
		NetworkMappings: s.Base.NetworkMappings,
		Configuration:   s.Configuration,
		State:           &builderv0.InitStatus{State: builderv0.InitStatus_SUCCESS}}, nil
}

func (s *BuilderWrapper) InitError(err error) (*builderv0.InitResponse, error) {
	if !s.loaded {
		return s.InitError(fmt.Errorf("not loaded"))
	}
	return &builderv0.InitResponse{
		State: &builderv0.InitStatus{State: builderv0.InitStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *BuilderWrapper) CreateResponse(ctx context.Context, settings any) (*builderv0.CreateResponse, error) {
	if !s.loaded {
		return s.CreateError(fmt.Errorf("not loaded"))
	}
	// Save settings
	err := s.Service.UpdateSpecFromSettings(settings)
	if err != nil {
		return s.CreateError(err)
	}

	// Save endpoints
	s.Service.Endpoints, err = configurations.FromProtoEndpoints(s.Endpoints...)
	if err != nil {
		return s.CreateError(err)
	}

	err = s.Service.Save(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "base: cannot save configuration")
	}
	return &builderv0.CreateResponse{
		Endpoints: s.Endpoints,
	}, nil
}

func (s *BuilderWrapper) CreateError(err error) (*builderv0.CreateResponse, error) {
	return &builderv0.CreateResponse{
		State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *BuilderWrapper) UpdateResponse() (*builderv0.UpdateResponse, error) {
	if !s.loaded {
		return s.UpdateError(fmt.Errorf("not loaded"))
	}
	return &builderv0.UpdateResponse{
		State: &builderv0.UpdateStatus{State: builderv0.UpdateStatus_SUCCESS},
	}, nil

}

func (s *BuilderWrapper) UpdateError(err error) (*builderv0.UpdateResponse, error) {
	return &builderv0.UpdateResponse{
		State: &builderv0.UpdateStatus{State: builderv0.UpdateStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *BuilderWrapper) SyncResponse() (*builderv0.SyncResponse, error) {
	if !s.loaded {
		return s.SyncError(fmt.Errorf("not loaded"))
	}
	return &builderv0.SyncResponse{
		State: &builderv0.SyncStatus{State: builderv0.SyncStatus_SUCCESS}}, nil
}

func (s *BuilderWrapper) SyncError(err error) (*builderv0.SyncResponse, error) {
	return &builderv0.SyncResponse{
		State: &builderv0.SyncStatus{State: builderv0.SyncStatus_ERROR, Message: err.Error()}}, err
}

func (s *BuilderWrapper) BuildResponse() (*builderv0.BuildResponse, error) {
	if !s.loaded {
		return s.BuildError(fmt.Errorf("not loaded"))
	}
	return &builderv0.BuildResponse{}, nil
}

func (s *BuilderWrapper) BuildError(err error) (*builderv0.BuildResponse, error) {
	return &builderv0.BuildResponse{
		State: &builderv0.BuildStatus{State: builderv0.BuildStatus_ERROR, Message: err.Error()}}, err
}

func (s *BuilderWrapper) DeployResponse() (*builderv0.DeploymentResponse, error) {
	if !s.loaded {
		return s.DeployError(fmt.Errorf("not loaded"))
	}
	return &builderv0.DeploymentResponse{}, nil
}

func (s *BuilderWrapper) DeployError(err error) (*builderv0.DeploymentResponse, error) {
	return &builderv0.DeploymentResponse{
		State: &builderv0.DeploymentStatus{State: builderv0.DeploymentStatus_ERROR, Message: err.Error()}}, err
}

type DeploymentBase struct {
	*Information
	Namespace   string
	Environment *configurations.Environment
	Image       *configurations.DockerImage
	Replicas    int

	// Specialization
	Parameters any
}

func (s *BuilderWrapper) CreateDeploymentBase(env *basev0.Environment, namespace string, builderContext *builderv0.BuildContext) *DeploymentBase {
	envInfo := configurations.EnvironmentFromProto(env)
	return &DeploymentBase{
		Namespace:   namespace,
		Information: s.Information,
		Environment: envInfo,
		Image:       s.DockerImage(builderContext),
		Replicas:    1,
	}
}

type EnvironmentMap map[string]string

type Parameters struct {
	Values map[string]string
}

type DeploymentParameters struct {
	ConfigMap  EnvironmentMap
	SecretMap  EnvironmentMap
	Parameters any
}

func EnvsAsConfigMapData(envs ...string) (EnvironmentMap, error) {
	m := make(EnvironmentMap)
	for _, env := range envs {
		key, value, err := ToKeyAndValue(env)
		if err != nil {
			return nil, err
		}
		m[key] = value
	}
	return m, nil
}

func ToKeyAndValue(env string) (string, string, error) {
	split := strings.SplitN(env, "=", 2)
	if len(split) != 2 {
		return "", "", fmt.Errorf("invalid env: %s", env)
	}
	return split[0], split[1], nil
}

func EnvsAsSecretData(envs ...string) (EnvironmentMap, error) {
	m := make(EnvironmentMap)
	for _, env := range envs {
		key, value, err := ToKeyAndValue(env)
		if err != nil {
			return nil, err
		}
		m[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	return m, nil
}

// GenericServiceDeploy is a generic deployment method that can be used to deploy many kind of service
func (s *BuilderWrapper) GenericServiceDeploy(ctx context.Context, req *builderv0.DeploymentRequest, fs embed.FS, params any) error {
	defer s.Wool.Catch()
	base := s.CreateDeploymentBase(req.Environment, req.Deployment.Namespace, req.BuildContext)

	switch v := req.Deployment.Kind.(type) {
	case *builderv0.Deployment_Kustomize:
		err := s.Builder.GenerateGenericKustomize(ctx, fs, v.Kustomize, base, params)
		if err != nil {
			return err
		}
	default:
		return s.Wool.Wrapf(fmt.Errorf("unsupported deployment kind: %T", v), "cannot deploy")

	}
	return nil
}

func WithFactory(fs embed.FS) *TemplateWrapper {
	return &TemplateWrapper{fs: shared.Embed(fs), dir: "templates/factory"}
}

func WithBuilder(fs embed.FS) *TemplateWrapper {
	return &TemplateWrapper{fs: shared.Embed(fs), dir: "templates/builder", relative: "builder"}
}

func WithDeployment(fs embed.FS, sub string) *TemplateWrapper {
	return &TemplateWrapper{
		fs: shared.Embed(fs), dir: fmt.Sprintf("templates/deployment/%s", sub), relative: "deployment"}
}

type DeploymentWrapper struct {
	*DeploymentBase
	Parameters any
}

func (s *BuilderWrapper) GenerateGenericKustomize(ctx context.Context, fs embed.FS, kust *builderv0.KustomizeDeployment, base *DeploymentBase, params any) error {
	wrapper := &DeploymentWrapper{DeploymentBase: base, Parameters: params}
	destination := path.Join(kust.Destination, "applications", s.Service.Application, "services", s.Service.Name)
	// Delete
	err := shared.EmptyDir(destination)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot empty destination")
	}
	err = s.Templates(ctx, wrapper,
		WithDeployment(fs, "kustomize/base").WithDestination(path.Join(destination, "base")),
		WithDeployment(fs, "kustomize/overlays/environment").WithDestination(path.Join(destination, "overlays", base.Environment.Name)),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *BuilderWrapper) LogInitRequest(req *builderv0.InitRequest) {
	w := s.Wool.In("builder::init")
	w.Focus("input",
		wool.Field("configurations", configurations.MakeConfigurationSummary(req.Configuration)),
		wool.Field("dependencies configurations", configurations.MakeManyConfigurationSummary(req.DependenciesConfigurations)),
		wool.Field("dependency endpoints", configurations.MakeManyEndpointSummary(req.DependenciesEndpoints)),
		wool.Field("network mapping", configurations.MakeManyNetworkMappingSummary(req.ProposedNetworkMappings)))
}

func (s *BuilderWrapper) LogDeployRequest(req *builderv0.DeploymentRequest) {
	w := s.Wool.In("runtime::init")
	w.Focus("input",
		wool.Field("other network mappings", configurations.MakeManyNetworkMappingSummary(req.OtherNetworkMappings)),
	)
}
