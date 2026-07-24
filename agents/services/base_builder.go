package services

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"

	serviceaudit "github.com/codefly-dev/core/agents/services/audit"
	servicesbom "github.com/codefly-dev/core/agents/services/sbom"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/failures"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/shared"
)

type BuilderWrapper struct {
	*Base

	BuildResult  *builderv0.BuildResult
	DeployOutput *builderv0.DeploymentOutput

	GettingStarted string

	CreationMode *builderv0.CreationMode
	SyncMode     *builderv0.SyncMode
}

// BuilderLoad describes the small set of choices in a conventional plugin
// Builder.Load implementation. Endpoint resolution is the plugin-specific
// seam; the base owns identity/settings loading, creation-mode documentation,
// endpoint loading, dependency localization, and structured responses.
type BuilderLoad struct {
	Settings         any
	Requirements     *builders.Dependencies
	FactoryTemplates fs.FS
	ResolveEndpoints func(context.Context, []*basev0.Endpoint) error
}

// LoadService implements the conventional service builder Load lifecycle.
func (s *BuilderWrapper) LoadService(ctx context.Context, req *builderv0.LoadRequest, load BuilderLoad) (*builderv0.LoadResponse, error) {
	if req == nil || req.GetIdentity() == nil {
		return s.LoadError(fmt.Errorf("builder load requires a service identity"))
	}
	if err := s.Base.Load(ctx, req.GetIdentity(), load.Settings); err != nil {
		return s.LoadError(err)
	}
	ctx = s.Wool.Inject(ctx)
	if req.GetDisableCatch() {
		s.Wool.DisableCatch()
	}
	if load.Requirements != nil {
		load.Requirements.Localize(s.Location)
	}
	if req.GetCreationMode() != nil {
		s.CreationMode = req.GetCreationMode()
		if load.FactoryTemplates != nil {
			gettingStarted, err := templates.ApplyTemplateFrom(ctx, shared.Embed(load.FactoryTemplates), "templates/factory/GETTING_STARTED.md", s.Information)
			if err != nil {
				return s.LoadError(err)
			}
			s.GettingStarted = gettingStarted
		}
		return s.LoadResponse()
	}

	endpoints, err := s.Service.LoadEndpoints(ctx)
	if err != nil {
		return s.LoadError(err)
	}
	s.Endpoints = endpoints
	if load.ResolveEndpoints != nil {
		if err = load.ResolveEndpoints(ctx, endpoints); err != nil {
			return s.LoadError(err)
		}
	}
	return s.LoadResponse()
}

func ErrorMessage(err error, msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	return fmt.Sprintf("%s: %s", msg, err)
}

func operationFailure(operation string, err error, message string) *basev0.Failure {
	failure := failures.FromError(operation, err)
	if failure == nil && message != "" {
		failure = failures.New(basev0.FailureCode_FAILURE_CODE_INTERNAL, operation, message)
	}
	if failure != nil && message != "" {
		failure.Message = message
	}
	return failure
}

func (s *BuilderWrapper) LoadResponse() (*builderv0.LoadResponse, error) {
	if !s.loaded {
		return s.LoadError(fmt.Errorf("not loaded"))
	}
	for _, e := range s.Endpoints {
		e.Module = s.Identity.Module
		e.Service = s.Identity.Name
	}
	return &builderv0.LoadResponse{
		Version:        s.Version(),
		Endpoints:      s.Endpoints,
		GettingStarted: s.GettingStarted,
		State:          &builderv0.LoadStatus{State: builderv0.LoadStatus_READY},
	}, nil
}

func (s *BuilderWrapper) LoadError(err error) (*builderv0.LoadResponse, error) {
	return &builderv0.LoadResponse{
		State: &builderv0.LoadStatus{State: builderv0.LoadStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.load", err, err.Error())},
	}, nil
}

func (s *BuilderWrapper) LoadErrorf(err error, msg string, args ...any) (*builderv0.LoadResponse, error) {
	message := ErrorMessage(err, msg, args...)
	return &builderv0.LoadResponse{
		State: &builderv0.LoadStatus{State: builderv0.LoadStatus_ERROR, Message: message, Failure: operationFailure("builder.load", err, message)},
	}, nil
}

func (s *BuilderWrapper) InitResponse() (*builderv0.InitResponse, error) {
	if !s.loaded {
		return s.InitError(fmt.Errorf("not loaded"))
	}
	return &builderv0.InitResponse{
		State: &builderv0.InitStatus{State: builderv0.InitStatus_SUCCESS},
	}, nil
}

func (s *BuilderWrapper) InitError(err error) (*builderv0.InitResponse, error) {
	return &builderv0.InitResponse{
		State: &builderv0.InitStatus{State: builderv0.InitStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.init", err, err.Error())},
	}, nil
}

func (s *BuilderWrapper) InitErrorf(err error, msg string, args ...any) (*builderv0.InitResponse, error) {
	message := ErrorMessage(err, msg, args...)
	return &builderv0.InitResponse{
		State: &builderv0.InitStatus{State: builderv0.InitStatus_ERROR, Message: message, Failure: operationFailure("builder.init", err, message)},
	}, nil
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
	s.Service.Endpoints, err = resources.FromProtoEndpoints(s.Endpoints...)
	if err != nil {
		return s.CreateError(err)
	}

	err = s.Service.Save(ctx)
	if err != nil {
		return s.CreateErrorf(err, "base: cannot save configuration")
	}
	return &builderv0.CreateResponse{
		Endpoints: s.Endpoints,
		State:     &builderv0.CreateStatus{State: builderv0.CreateStatus_CREATED},
	}, nil
}

func (s *BuilderWrapper) CreateError(err error) (*builderv0.CreateResponse, error) {
	return &builderv0.CreateResponse{
		State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.create", err, err.Error())},
	}, nil
}

func (s *BuilderWrapper) CreateErrorf(err error, msg string, args ...any) (*builderv0.CreateResponse, error) {
	message := ErrorMessage(err, msg, args...)
	return &builderv0.CreateResponse{
		State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: message, Failure: operationFailure("builder.create", err, message)},
	}, nil
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
		State: &builderv0.UpdateStatus{State: builderv0.UpdateStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.update", err, err.Error())},
	}, nil
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
		State: &builderv0.SyncStatus{State: builderv0.SyncStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.sync", err, err.Error())}}, nil
}

// SyncUnsupported reports that the requested synchronization mode cannot be
// performed authoritatively. CI dry-run callers must never infer "no drift"
// from an agent that only implements mutating Sync.
func (s *BuilderWrapper) SyncUnsupported(message string) (*builderv0.SyncResponse, error) {
	return &builderv0.SyncResponse{
		State: &builderv0.SyncStatus{State: builderv0.SyncStatus_UNSUPPORTED, Message: message, Failure: failures.New(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "builder.sync", message)}}, nil
}

func (s *BuilderWrapper) WithDockerImages(ims ...*resources.DockerImage) {
	var imgs []string
	for _, im := range ims {
		imgs = append(imgs, im.FullName())

	}
	s.Builder.BuildResult = &builderv0.BuildResult{
		Kind: &builderv0.BuildResult_DockerBuildResult{
			DockerBuildResult: &builderv0.DockerBuildResult{
				Images: imgs,
			},
		},
	}
}

func (s *BuilderWrapper) BuildResponse() (*builderv0.BuildResponse, error) {
	if !s.loaded {
		return s.BuildError(fmt.Errorf("not loaded"))
	}
	resp := &builderv0.BuildResponse{State: &builderv0.BuildStatus{State: builderv0.BuildStatus_SUCCESS}}
	if s.BuildResult != nil {
		resp.Result = s.BuildResult
	}
	return resp, nil
}

func (s *BuilderWrapper) BuildError(err error) (*builderv0.BuildResponse, error) {
	return &builderv0.BuildResponse{
		State: &builderv0.BuildStatus{State: builderv0.BuildStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.build", err, err.Error())}}, nil
}

func (s *BuilderWrapper) DeployResponse() (*builderv0.DeploymentResponse, error) {
	if !s.loaded {
		return s.DeployError(fmt.Errorf("not loaded"))
	}
	return &builderv0.DeploymentResponse{
		Configuration: s.Configuration,
		Deployment:    s.DeployOutput,
		State:         &builderv0.DeploymentStatus{State: builderv0.DeploymentStatus_SUCCESS},
	}, nil
}

func (s *BuilderWrapper) DeployError(err error) (*builderv0.DeploymentResponse, error) {
	return &builderv0.DeploymentResponse{
		State: &builderv0.DeploymentStatus{State: builderv0.DeploymentStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.deploy", err, err.Error())}}, nil
}

// AuditResponse builds a successful AuditResponse. State is CLEAN if
// findings is empty, FINDINGS otherwise. Tool/language identify the
// scanner used so the CLI can render "[govulncheck+go list -u] go-grpc/api"
// in mixed-workspace audits.
func (s *BuilderWrapper) AuditResponse(req *builderv0.AuditRequest, findings []*builderv0.AuditFinding, outdated []*builderv0.OutdatedDep, tool, language string) (*builderv0.AuditResponse, error) {
	state := builderv0.AuditStatus_CLEAN
	message := ""
	var failure *basev0.Failure
	if len(findings) > 0 {
		state = builderv0.AuditStatus_FINDINGS
	}
	if req.GetFailOnVuln() {
		for _, finding := range findings {
			if finding.GetSeverity() >= builderv0.AuditFinding_HIGH {
				state = builderv0.AuditStatus_ERROR
				message = "audit found HIGH or CRITICAL vulnerabilities"
				failure = failures.New(basev0.FailureCode_FAILURE_CODE_SECURITY_POLICY_FAILED, "builder.audit", message)
				break
			}
		}
	}
	return &builderv0.AuditResponse{
		State:    &builderv0.AuditStatus{State: state, Message: message, Failure: failure},
		Findings: findings,
		Outdated: outdated,
		Tool:     tool,
		Language: language,
	}, nil
}

func (s *BuilderWrapper) AuditError(err error) (*builderv0.AuditResponse, error) {
	return &builderv0.AuditResponse{
		State: &builderv0.AuditStatus{State: builderv0.AuditStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.audit", err, err.Error())},
	}, nil
}

func (s *BuilderWrapper) AuditErrorf(err error, msg string, args ...any) (*builderv0.AuditResponse, error) {
	message := ErrorMessage(err, msg, args...)
	return &builderv0.AuditResponse{
		State: &builderv0.AuditStatus{State: builderv0.AuditStatus_ERROR, Message: message, Failure: operationFailure("builder.audit", err, message)},
	}, nil
}

// AuditUnsupported reports that this agent has no authoritative scanner.
func (s *BuilderWrapper) AuditUnsupported(message string) (*builderv0.AuditResponse, error) {
	return &builderv0.AuditResponse{
		State: &builderv0.AuditStatus{State: builderv0.AuditStatus_UNSUPPORTED, Message: message, Failure: failures.New(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "builder.audit", message)},
	}, nil
}

// AuditContainer is the shared implementation for stock-image service agents.
func (s *BuilderWrapper) AuditContainer(ctx context.Context, req *builderv0.AuditRequest, image string) (*builderv0.AuditResponse, error) {
	result, err := serviceaudit.Docker(ctx, image)
	if err != nil {
		return s.AuditError(err)
	}
	return s.AuditResponse(req, result.Findings, result.Outdated, result.Tool, result.Language)
}

// SBOMResponse builds a complete, authoritative CycloneDX response.
func (s *BuilderWrapper) SBOMResponse(bom *agentv0.Bom, tool, language, sha256 string) (*builderv0.SBOMResponse, error) {
	return &builderv0.SBOMResponse{
		State:    &builderv0.SBOMStatus{State: builderv0.SBOMStatus_COMPLETE},
		Bom:      bom,
		Tool:     tool,
		Language: language,
		Sha256:   sha256,
	}, nil
}

// SBOMError reports a failed inventory. It never returns an empty COMPLETE
// response because incomplete inventories are unsafe release evidence.
func (s *BuilderWrapper) SBOMError(err error) (*builderv0.SBOMResponse, error) {
	return &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.sbom", err, err.Error())},
	}, nil
}

// SBOMUnsupported reports that this plugin has no authoritative generator.
func (s *BuilderWrapper) SBOMUnsupported(message string) (*builderv0.SBOMResponse, error) {
	return &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_UNSUPPORTED, Message: message, Failure: failures.New(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "builder.sbom", message)},
	}, nil
}

// SBOMContainer is the shared implementation for stock-image service agents.
func (s *BuilderWrapper) SBOMContainer(ctx context.Context, image string) (*builderv0.SBOMResponse, error) {
	result, err := servicesbom.Container(ctx, image)
	if err != nil {
		return s.SBOMError(err)
	}
	return s.SBOMResponse(result.Bom, result.Tool, result.Language, result.SHA256)
}

// PackageResponse builds a successful portable-package response.
func (s *BuilderWrapper) PackageResponse(artifacts []*builderv0.PackageArtifact) (*builderv0.PackageResponse, error) {
	if !s.loaded {
		return s.PackageError(fmt.Errorf("not loaded"))
	}
	return &builderv0.PackageResponse{
		State:     &builderv0.PackageStatus{State: builderv0.PackageStatus_SUCCESS},
		Artifacts: artifacts,
	}, nil
}

// PackageError reports a failed portable-package operation.
func (s *BuilderWrapper) PackageError(err error) (*builderv0.PackageResponse, error) {
	return &builderv0.PackageResponse{
		State: &builderv0.PackageStatus{
			State:   builderv0.PackageStatus_ERROR,
			Message: err.Error(),
			Failure: operationFailure("builder.package", err, err.Error()),
		},
	}, nil
}

// PackageUnsupported reports that the plugin cannot package its loaded source.
func (s *BuilderWrapper) PackageUnsupported(message string) (*builderv0.PackageResponse, error) {
	return &builderv0.PackageResponse{
		State: &builderv0.PackageStatus{
			State:   builderv0.PackageStatus_UNSUPPORTED,
			Message: message,
			Failure: failures.New(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "builder.package", message),
		},
	}, nil
}

// UpgradeResponse builds a successful UpgradeResponse. State is NOOP if
// no changes were applied (or would be, in dry-run), SUCCESS otherwise.
func (s *BuilderWrapper) UpgradeResponse(changes []*builderv0.UpgradeChange, lockfileDiff string) (*builderv0.UpgradeResponse, error) {
	state := builderv0.UpgradeStatus_SUCCESS
	if len(changes) == 0 {
		state = builderv0.UpgradeStatus_NOOP
	}
	return &builderv0.UpgradeResponse{
		State:        &builderv0.UpgradeStatus{State: state},
		Changes:      changes,
		LockfileDiff: lockfileDiff,
	}, nil
}

func (s *BuilderWrapper) UpgradeError(err error) (*builderv0.UpgradeResponse, error) {
	return &builderv0.UpgradeResponse{
		State: &builderv0.UpgradeStatus{State: builderv0.UpgradeStatus_ERROR, Message: err.Error(), Failure: operationFailure("builder.upgrade", err, err.Error())},
	}, nil
}

func (s *BuilderWrapper) UpgradeErrorf(err error, msg string, args ...any) (*builderv0.UpgradeResponse, error) {
	message := ErrorMessage(err, msg, args...)
	return &builderv0.UpgradeResponse{
		State: &builderv0.UpgradeStatus{State: builderv0.UpgradeStatus_ERROR, Message: message, Failure: operationFailure("builder.upgrade", err, message)},
	}, nil
}

type DeploymentBase struct {
	*Information
	Sha         string
	Namespace   string
	Environment *resources.Environment
	Image       *resources.DockerImage
	Replicas    int

	// Specialization
	Parameters any
}

// ImageIDResolver resolves a locally-built Docker image's ID (digest) for
// deployment SHA stamping. It is injected by the CLI (which links the Docker
// client) via runners/dockerrun.GetImageID; left nil inside agent processes so
// package services stays free of the Docker client. See CreateKubernetesBase.
var ImageIDResolver func(*resources.DockerImage) (string, error)

func (s *BuilderWrapper) CreateKubernetesBase(_ context.Context, env *basev0.Environment, namespace string, builderContext *builderv0.DockerBuildContext) (*DeploymentBase, error) {
	envInfo := resources.EnvironmentFromProto(env)
	dockerImage := s.DockerImage(builderContext)

	// Try to get local image SHA. For stock/external images that aren't
	// built locally, fall back to the tag as the SHA marker.
	//
	// Image-ID resolution needs the Docker client, which we deliberately do
	// NOT link into this package: package services is compiled into every
// agent binary, and pulling in the Moby client would drag the
	// whole Moby module (and its daemon-side, unpatchable CVEs) into agents
	// that never touch Docker. Instead the CLI — which legitimately links the
	// Docker client — injects ImageIDResolver at startup. When it is unset
	// (e.g. inside an agent process), we fall back to the tag, which is the
	// same path stock/non-local images already take.
	sha := dockerImage.Tag
	if ImageIDResolver != nil {
		if id, err := ImageIDResolver(dockerImage); err == nil {
			if trimmed, ok := strings.CutPrefix(id, "sha256:"); ok {
				sha = trimmed[:12]
			}
		} else {
			s.Wool.Debug("image not found locally, using tag as sha", wool.Field("image", dockerImage.FullName()))
		}
	}

	return &DeploymentBase{
		Sha:         sha,
		Namespace:   namespace,
		Information: s.Information,
		Environment: envInfo,
		Image:       dockerImage,
		Replicas:    1,
	}, nil
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

// DeploymentInputs declares which standard Codefly inputs a Kubernetes
// workload consumes. Keeping this policy explicit lets resource plugins avoid
// receiving application-only values while application plugins can opt into the
// complete, conventional environment with one helper.
type DeploymentInputs struct {
	OwnEndpoints             bool
	DependencyEndpoints      bool
	OwnConfiguration         bool
	DependencyConfigurations bool
}

// ApplicationDeploymentInputs returns the conventional inputs for an
// application workload: its own container-local endpoints plus dependency
// endpoints and both service and dependency configuration.
func ApplicationDeploymentInputs() DeploymentInputs {
	return DeploymentInputs{
		OwnEndpoints:             true,
		DependencyEndpoints:      true,
		OwnConfiguration:         true,
		DependencyConfigurations: true,
	}
}

// KustomizeDeployment describes the common deployment pipeline used by
// Codefly service plugins. Prepare is the plugin-specific seam: managed
// resources typically use it to derive and export their connection
// configuration, while gateways use it to generate route configuration and
// set Parameters.
type KustomizeDeployment struct {
	EnvironmentVariables *resources.EnvironmentVariableManager
	Templates            fs.FS
	Inputs               DeploymentInputs
	Parameters           any
	Prepare              func(context.Context, *KustomizeDeploymentContext) error
}

// KustomizeDeploymentContext is passed to a plugin's Prepare hook after the
// requested standard inputs have been collected and before ConfigMap/Secret
// data is rendered.
type KustomizeDeploymentContext struct {
	Builder              *BuilderWrapper
	Request              *builderv0.DeploymentRequest
	Kubernetes           *builderv0.KubernetesDeployment
	EnvironmentVariables *resources.EnvironmentVariableManager
	Parameters           any
	ConfigMap            []*resources.EnvironmentVariable
	Secrets              []*resources.EnvironmentVariable
}

// ExportConfiguration publishes a resource's connection configuration in the
// deployment response and makes its values available to the rendered
// workload. This is the standard operation for managed resource plugins.
func (d *KustomizeDeploymentContext) ExportConfiguration(ctx context.Context, configuration *basev0.Configuration) error {
	if configuration == nil {
		return fmt.Errorf("cannot export a nil configuration")
	}
	d.Builder.Configuration = configuration
	return d.EnvironmentVariables.AddConfigurations(ctx, configuration)
}

// AddConfigMap appends raw, non-secret values to the generated ConfigMap.
func (d *KustomizeDeploymentContext) AddConfigMap(values ...*resources.EnvironmentVariable) {
	d.ConfigMap = append(d.ConfigMap, values...)
}

// AddSecrets appends raw secret values to the generated Kubernetes Secret.
func (d *KustomizeDeploymentContext) AddSecrets(values ...*resources.EnvironmentVariable) {
	d.Secrets = append(d.Secrets, values...)
}

func EnvsAsConfigMapData(envs ...*resources.EnvironmentVariable) (EnvironmentMap, error) {
	m := make(EnvironmentMap)
	for _, env := range envs {
		m[env.Key] = env.ValueAsString()
	}
	return m, nil
}

func EnvsAsSecretData(envs ...*resources.EnvironmentVariable) (EnvironmentMap, error) {
	m := make(EnvironmentMap)
	for _, env := range envs {
		m[env.Key] = env.ValueAsEncodedString()
	}
	return m, nil
}

func (s *BuilderWrapper) KubernetesDeploymentRequest(_ context.Context, req *builderv0.DeploymentRequest) (*builderv0.KubernetesDeployment, error) {
	if req == nil {
		return nil, s.Wool.Wrapf(fmt.Errorf("deployment request is nil"), "cannot deploy")
	}
	if req.Deployment == nil {
		return nil, s.Wool.Wrapf(fmt.Errorf("deployment target is missing"), "cannot deploy")
	}
	switch v := req.Deployment.Kind.(type) {
	case *builderv0.Deployment_Kubernetes:
		if v.Kubernetes == nil {
			return nil, s.Wool.Wrapf(fmt.Errorf("kubernetes deployment is missing"), "cannot deploy")
		}
		s.DeployOutput = KustomizeOutput()
		return v.Kubernetes, nil
	default:
		return nil, s.Wool.Wrapf(fmt.Errorf("unsupported deployment kind: %T", v), "cannot deploy")
	}
}

// DeployKustomize runs the standard service-plugin Kubernetes deployment
// pipeline: validate the target, collect declared inputs, run the plugin's
// preparation hook, split configuration from secrets, render Kustomize, and
// return the structured builder response.
func (s *BuilderWrapper) DeployKustomize(ctx context.Context, req *builderv0.DeploymentRequest, deployment KustomizeDeployment) (*builderv0.DeploymentResponse, error) {
	if deployment.EnvironmentVariables == nil {
		return s.DeployError(fmt.Errorf("kustomize deployment requires an environment variable manager"))
	}
	if deployment.Templates == nil {
		return s.DeployError(fmt.Errorf("kustomize deployment requires templates"))
	}

	kubernetes, err := s.KubernetesDeploymentRequest(ctx, req)
	if err != nil {
		return s.DeployError(err)
	}

	s.LogDeployRequest(req, s.Wool.Debug)
	manager := deployment.EnvironmentVariables
	manager.SetRunning()

	if deployment.Inputs.OwnEndpoints {
		err = manager.AddEndpoints(ctx,
			resources.LocalizeNetworkMapping(req.GetNetworkMappings(), "localhost"),
			resources.NewContainerNetworkAccess())
		if err != nil {
			return s.DeployError(err)
		}
	}
	if deployment.Inputs.DependencyEndpoints {
		err = manager.AddEndpoints(ctx, req.GetDependenciesNetworkMappings(), resources.NewContainerNetworkAccess())
		if err != nil {
			return s.DeployError(err)
		}
	}
	if deployment.Inputs.OwnConfiguration {
		if err = manager.AddConfigurations(ctx, req.GetConfiguration()); err != nil {
			return s.DeployError(err)
		}
	}
	if deployment.Inputs.DependencyConfigurations {
		if err = manager.AddConfigurations(ctx, req.GetDependenciesConfigurations()...); err != nil {
			return s.DeployError(err)
		}
	}

	deploymentContext := &KustomizeDeploymentContext{
		Builder:              s,
		Request:              req,
		Kubernetes:           kubernetes,
		EnvironmentVariables: manager,
		Parameters:           deployment.Parameters,
	}
	if deployment.Prepare != nil {
		if err = deployment.Prepare(ctx, deploymentContext); err != nil {
			return s.DeployError(err)
		}
	}

	configurations, err := manager.Configurations()
	if err != nil {
		return s.DeployError(err)
	}
	configurations = append(configurations, deploymentContext.ConfigMap...)
	configMap, err := EnvsAsConfigMapData(configurations...)
	if err != nil {
		return s.DeployError(err)
	}
	secrets := append(manager.Secrets(), deploymentContext.Secrets...)
	secretMap, err := EnvsAsSecretData(secrets...)
	if err != nil {
		return s.DeployError(err)
	}

	parameters := DeploymentParameters{
		ConfigMap:  configMap,
		SecretMap:  secretMap,
		Parameters: deploymentContext.Parameters,
	}
	if err = s.KustomizeDeploy(ctx, req.GetEnvironment(), kubernetes, deployment.Templates, parameters); err != nil {
		return s.DeployError(err)
	}
	return s.DeployResponse()
}

func KustomizeOutput() *builderv0.DeploymentOutput {
	return &builderv0.DeploymentOutput{
		Kind: &builderv0.DeploymentOutput_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeploymentOutput{
				Kind: builderv0.KubernetesDeploymentOutput_KUSTOMIZE,
			},
		},
	}
}

func (s *BuilderWrapper) KustomizeDeploy(ctx context.Context, env *basev0.Environment, req *builderv0.KubernetesDeployment, fsys fs.FS, params any) error {
	defer s.Wool.Catch()

	b, err := s.CreateKubernetesBase(ctx, env, req.Namespace, req.BuildContext)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create base")
	}
	err = s.Builder.GenerateGenericKustomize(ctx, fsys, req, b, params)
	if err != nil {
		return err
	}
	return nil
}

// WithFactory scaffolds a service from the factory templates. It DOES NOT
// overwrite files that already exist on disk (Override: SkipAll) — factory
// templates seed a NEW service, so re-running Create / a Sync over an existing
// service must preserve the user's edits rather than clobber them (the old
// nil-Override default silently truncated every existing file). Agents that
// genuinely need to overwrite specific files set an explicit .WithOverride(...)
// — e.g. go-grpc overwrites everything except *.proto.
func WithFactory(fsys fs.FS) *TemplateWrapper {
	return &TemplateWrapper{fs: shared.Embed(fsys), dir: "templates/factory", Override: shared.SkipAll()}
}

// WithBuilder renders build-time templates (Dockerfile, etc.). These are
// regenerated artifacts, not user-editable, so they overwrite by default.
func WithBuilder(fsys fs.FS) *TemplateWrapper {
	return &TemplateWrapper{fs: shared.Embed(fsys), dir: "templates/builder", relative: "builder"}
}

func WithDeployment(fsys fs.FS, sub string) *TemplateWrapper {
	return &TemplateWrapper{
		fs: shared.Embed(fsys), dir: fmt.Sprintf("templates/deployment/%s", sub), relative: "deployment"}
}

type DeploymentWrapper struct {
	*DeploymentBase
	Deployment any

	// Stable convenience aliases for plugin templates. These keep the golden
	// path shallow ({{ .Name }}, {{ range .SecretMap }}) while Deployment keeps
	// the complete plugin-specific parameter object available.
	Name      string
	ConfigMap EnvironmentMap
	SecretMap EnvironmentMap
}

func (s *BuilderWrapper) GenerateGenericKustomize(ctx context.Context, fsys fs.FS, k *builderv0.KubernetesDeployment, base *DeploymentBase, params any) error {
	wrapper := &DeploymentWrapper{DeploymentBase: base, Deployment: params}
	if base.Information != nil && base.Information.Service != nil {
		wrapper.Name = base.Information.Service.Name.DNSCase
	}
	switch deployment := params.(type) {
	case DeploymentParameters:
		wrapper.ConfigMap = deployment.ConfigMap
		wrapper.SecretMap = deployment.SecretMap
	case *DeploymentParameters:
		if deployment != nil {
			wrapper.ConfigMap = deployment.ConfigMap
			wrapper.SecretMap = deployment.SecretMap
		}
	}
	// Delete
	err := shared.EmptyDir(ctx, k.Destination)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot empty destination")
	}
	err = s.Templates(ctx, wrapper,
		WithDeployment(fsys, "kustomize/base").WithDestination("%s", path.Join(k.Destination, "base")),
		WithDeployment(fsys, "kustomize/overlays/environment").WithDestination("%s", path.Join(k.Destination, "overlays", base.Environment.Name)),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *BuilderWrapper) LogInitRequest(req *builderv0.InitRequest) {
	w := s.Wool.In("builder::init")
	w.Debug("input",
		wool.Field("dependency endpoints", resources.MakeManyEndpointSummary(req.GetDependenciesEndpoints())),
	)
}

func (s *BuilderWrapper) LogDeployRequest(req *builderv0.DeploymentRequest, log wool.LogFunc) {
	if req == nil {
		log("input", wool.Field("request", "nil"))
		return
	}
	log("input",
		wool.Field("configuration", resources.MakeConfigurationSummary(req.GetConfiguration())),
		wool.Field("dependencies configurations", resources.MakeManyConfigurationSummary(req.GetDependenciesConfigurations())),
		wool.Field("network mappings", resources.MakeManyNetworkMappingSummary(req.GetNetworkMappings())),
		wool.Field("dependencies network mappings", resources.MakeManyNetworkMappingSummary(req.GetDependenciesNetworkMappings())),
	)
}

func (s *BuilderWrapper) DockerBuildRequest(_ context.Context, req *builderv0.BuildRequest) (*builderv0.DockerBuildContext, error) {
	switch v := req.BuildContext.Kind.(type) {
	case *builderv0.BuildContext_DockerBuildContext:
		return v.DockerBuildContext, nil
	default:
		return nil, s.Wool.Wrapf(fmt.Errorf("unsupported build context kind: %T", v), "cannot build")
	}
}
