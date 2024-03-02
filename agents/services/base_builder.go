package services

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
	"github.com/codefly-dev/core/shared"
)

type BuilderWrapper struct {
	*Base
}

func (s *BuilderWrapper) LoadResponse(gettingStarted string) (*builderv0.LoadResponse, error) {
	if !s.loaded {
		return s.LoadError(fmt.Errorf("not loaded"))
	}
	for _, e := range s.Endpoints {
		e.Application = s.Identity.Application
		e.Service = s.Identity.Name
		e.Namespace = s.Identity.Namespace
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

func (s *BuilderWrapper) InitResponse(networMappings []*basev0.NetworkMapping, hash string) (*builderv0.InitResponse, error) {
	if !s.loaded {
		return s.InitError(fmt.Errorf("not loaded"))
	}
	return &builderv0.InitResponse{
		NetworkMappings: networMappings,
		RunHash:         hash,
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
	err := s.Configuration.UpdateSpecFromSettings(settings)
	if err != nil {
		return s.CreateError(err)
	}
	s.Configuration.Endpoints, err = configurations.FromProtoEndpoints(s.Endpoints...)
	if err != nil {
		return s.CreateError(err)
	}

	err = s.Configuration.Save(ctx)
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
	Environment *configurations.Environment
	Image       *configurations.DockerImage
	Replicas    int

	// Specialization
	Parameters any
}

func (s *BuilderWrapper) CreateDeploymentBase(env *configurations.Environment) *DeploymentBase {
	return &DeploymentBase{
		Information: s.Information,
		Environment: env,
		Image:       s.DockerImage(),
		Replicas:    1,
	}
}

type EnvironmentMap map[string]string

type DeploymentParameter struct {
	ConfigMap EnvironmentMap
	SecretMap EnvironmentMap
}

func EnvsAsConfigMapData(envs []string) map[string]string {
	m := make(map[string]string)
	for _, env := range envs {
		split := strings.SplitN(env, "=", 2)
		if len(split) == 2 {
			m[split[0]] = split[1]
		}
	}
	return m
}

func EnvsAsSecretData(envs []string) map[string]string {
	m := make(map[string]string)
	for _, env := range envs {
		split := strings.SplitN(env, "=", 2)
		if len(split) == 2 {
			m[split[0]] = base64.StdEncoding.EncodeToString([]byte(split[1]))
		}
	}
	return m
}

func (s *BuilderWrapper) Deploy(ctx context.Context, req *builderv0.DeploymentRequest, fs embed.FS, params any) error {
	defer s.Wool.Catch()
	env := configurations.EnvironmentFromProto(req.Environment)
	base := s.CreateDeploymentBase(env)
	for _, kind := range req.Deployments {
		switch v := kind.Deployment.(type) {
		case *builderv0.Deployment_Kustomize:
			err := s.Builder.GenerateKustomize(ctx, fs, v.Kustomize.Destination, base, params)
			if err != nil {
				return err
			}
		}
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

func (s *BuilderWrapper) GenerateKustomize(ctx context.Context, fs embed.FS, destination string, base *DeploymentBase, params any) error {
	wrapper := &DeploymentWrapper{DeploymentBase: base, Parameters: params}
	destination = path.Join(destination, "applications", s.Configuration.Application, "services", s.Configuration.Name)
	err := s.Templates(ctx, wrapper,
		WithDeployment(fs, "kustomize/base").WithDestination(path.Join(destination, "base")),
		WithDeployment(fs, "kustomize/overlays/environment").WithDestination(path.Join(destination, "overlays", base.Environment.Name)),
	)
	if err != nil {
		return err
	}
	return nil
}
