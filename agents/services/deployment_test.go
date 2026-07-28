package services

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/deployment
var deploymentTestFS embed.FS

func TestDeployKustomizeCollectsInputsAndRunsPreparation(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)

	manager := resources.NewEnvironmentVariableManager()
	manager.SetIdentity(&basev0.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"})
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder

	destination := t.TempDir()
	req := &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
			},
		}},
		Configuration: configuration("module/service", "application", "PLAIN", "value", false),
		DependenciesConfigurations: []*basev0.Configuration{
			configuration("module/database", "database", "PASSWORD", "dependency-secret", true),
		},
	}

	response, err := builder.DeployKustomize(ctx, req, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs: DeploymentInputs{
			OwnConfiguration:         true,
			DependencyConfigurations: true,
		},
		Parameters: struct{ Name string }{Name: "prepared"},
		Prepare: func(ctx context.Context, deployment *KustomizeDeploymentContext) error {
			exported := configuration("module/service", "connection", "URL", "redis://service", false)
			if err := deployment.ExportConfiguration(ctx, exported); err != nil {
				return err
			}
			deployment.AddConfigMap(resources.Env("EXTRA", "config"))
			deployment.AddSecrets(resources.Env("TOKEN", "raw-secret"))
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	require.Equal(t, "module/service", response.GetConfiguration().GetOrigin())
	require.Equal(t, builderv0.KubernetesDeploymentOutput_KUSTOMIZE, response.GetDeployment().GetKubernetes().GetKind())
	require.Equal(t, KubernetesManifestContractVersion, response.GetDeployment().GetKubernetes().GetContractVersion())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, response.GetDeployment().GetKubernetes().GetValidation().GetStaticValidation())
	require.False(t, response.GetDeployment().GetKubernetes().GetValidation().GetPromotable())

	configMapManifest, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	manifest := string(configMapManifest)
	for _, expected := range []string{
		`CODEFLY__RUNNING: "true"`,
		`CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__APPLICATION__PLAIN: "value"`,
		`CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__CONNECTION__URL: "redis://service"`,
		`EXTRA: "config"`,
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("ConfigMap missing %q:\n%s", expected, manifest)
		}
	}

	secretManifest, err := os.ReadFile(filepath.Join(destination, "base", "secret.yaml"))
	require.NoError(t, err)
	manifest = string(secretManifest)
	for _, expected := range []string{
		`CODEFLY__SERVICE_SECRET_CONFIGURATION__MODULE__DATABASE__DATABASE__PASSWORD: "ZGVwZW5kZW5jeS1zZWNyZXQ="`,
		`TOKEN: "cmF3LXNlY3JldA=="`,
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("Secret missing %q:\n%s", expected, manifest)
		}
	}

	deploymentManifest, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(deploymentManifest), `codefly.dev/test-parameter: "prepared"`)
}

func TestDeployKustomizeRejectsSecretBytesForGitOps(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	destination := t.TempDir()

	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			},
		}},
		Configuration: configuration("module/service", "application", "TOKEN", "must-not-render", true),
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               DeploymentInputs{OwnConfiguration: true},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "cannot receive secret value")
	entries, err := os.ReadDir(destination)
	require.NoError(t, err)
	require.Empty(t, entries)

	response, err = builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: t.TempDir(),
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Prepare: func(_ context.Context, deployment *KustomizeDeploymentContext) error {
			deployment.AddSecrets(resources.Env("TOKEN", "must-not-render"))
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "cannot receive secret values")
}

func TestDeployKustomizeRejectsSecretConfigurationDataForGitOps(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	destination := t.TempDir()
	prepareCalled := false

	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			},
		}},
		Configuration: &basev0.Configuration{
			Origin: "module/service",
			Infos: []*basev0.ConfigurationInformation{{
				Name: "credentials",
				Data: &basev0.ConfigurationData{
					Kind:    "yaml",
					Content: []byte("password: must-not-render"),
					Secret:  true,
				},
			}},
		},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "gitops"},
		Prepare: func(context.Context, *KustomizeDeploymentContext) error {
			prepareCalled = true
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "cannot receive secret data")
	require.False(t, prepareCalled)
	entries, err := os.ReadDir(destination)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDeployKustomizeRendersSecretFreeGitOpsTreeAndRejectsWithoutServerValidation(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	destination := t.TempDir()

	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
				SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{
					"DATABASE_PASSWORD": {Name: "service-secrets", Key: "database-password"},
				},
			},
		}},
		Configuration: configuration("module/service", "application", "PLAIN", "value", false),
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               DeploymentInputs{OwnConfiguration: true},
		Parameters:           struct{ Name string }{Name: "gitops"},
		Prepare: func(ctx context.Context, deployment *KustomizeDeploymentContext) error {
			return deployment.ExportConfiguration(ctx, configuration("module/service", "connection", "URL", "redis://service", false))
		},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "requires successful server-side validation")
	output := response.GetDeployment().GetKubernetes()
	require.Equal(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1, output.GetProfile())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, output.GetValidation().GetStaticValidation())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_NOT_RUN, output.GetValidation().GetServerSideValidation())
	require.False(t, output.GetValidation().GetPromotable())
	secretManifest, err := os.ReadFile(filepath.Join(destination, "base", "secret.yaml"))
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(string(secretManifest)))
	deploymentManifest, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(deploymentManifest), "must-not-render")
	require.Contains(t, string(deploymentManifest), "name: service-secrets")
	require.Contains(t, string(deploymentManifest), "key: database-password")
	configMapManifest, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(configMapManifest), `CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__CONNECTION__URL: "redis://service"`)
}

func TestDeployKustomizeDoesNotRetainSecretsBetweenRequests(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	manager.SetIdentity(&basev0.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"})
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder

	ephemeralResponse, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: t.TempDir(),
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
			},
		}},
		Configuration: configuration("module/service", "application", "TOKEN", "ephemeral-only", true),
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               DeploymentInputs{OwnConfiguration: true},
		Parameters:           struct{ Name string }{Name: "ephemeral"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, ephemeralResponse.GetState().GetState())

	gitOpsDestination := t.TempDir()
	gitOpsResponse, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: gitOpsDestination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
				SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{
					"TOKEN": {Name: "service-secrets", Key: "token"},
				},
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "gitops"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, gitOpsResponse.GetState().GetState())
	require.Contains(t, gitOpsResponse.GetState().GetMessage(), "requires successful server-side validation")
	require.NotContains(t, gitOpsResponse.GetState().GetMessage(), "cannot receive secret values")
	secretManifest, err := os.ReadFile(filepath.Join(gitOpsDestination, "base", "secret.yaml"))
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(string(secretManifest)))
}

func TestDeployKustomizeKeepsConcurrentOutputsRequestScoped(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder

	type deploymentResult struct {
		response *builderv0.DeploymentResponse
		err      error
	}
	ephemeralEntered := make(chan struct{})
	releaseEphemeral := make(chan struct{})
	gitOpsEntered := make(chan struct{})
	releaseGitOps := make(chan struct{})
	ephemeralResult := make(chan deploymentResult, 1)
	gitOpsResult := make(chan deploymentResult, 1)
	ephemeralDestination := t.TempDir()
	gitOpsDestination := t.TempDir()

	go func() {
		response, deployErr := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
			Environment: &basev0.Environment{Name: "test"},
			Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
				Kubernetes: &builderv0.KubernetesDeployment{
					Namespace:   "codefly",
					Destination: ephemeralDestination,
					Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
				},
			}},
		}, KustomizeDeployment{
			EnvironmentVariables: manager,
			Templates:            templates,
			Parameters:           struct{ Name string }{Name: "ephemeral"},
			Prepare: func(context.Context, *KustomizeDeploymentContext) error {
				close(ephemeralEntered)
				<-releaseEphemeral
				return nil
			},
		})
		ephemeralResult <- deploymentResult{response: response, err: deployErr}
	}()
	<-ephemeralEntered

	go func() {
		response, deployErr := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
			Environment: &basev0.Environment{Name: "test"},
			Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
				Kubernetes: &builderv0.KubernetesDeployment{
					Namespace:   "codefly",
					Destination: gitOpsDestination,
					Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
					BuildContext: &builderv0.DockerBuildContext{
						ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					},
				},
			}},
		}, KustomizeDeployment{
			EnvironmentVariables: manager,
			Templates:            templates,
			Parameters:           struct{ Name string }{Name: "gitops"},
			Prepare: func(context.Context, *KustomizeDeploymentContext) error {
				close(gitOpsEntered)
				<-releaseGitOps
				return nil
			},
		})
		gitOpsResult <- deploymentResult{response: response, err: deployErr}
	}()
	<-gitOpsEntered

	close(releaseEphemeral)
	ephemeral := <-ephemeralResult
	close(releaseGitOps)
	gitOps := <-gitOpsResult

	require.NoError(t, ephemeral.err)
	require.Equal(t,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
		ephemeral.response.GetDeployment().GetKubernetes().GetProfile(),
	)
	require.NoError(t, gitOps.err)
	require.Equal(t,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
		gitOps.response.GetDeployment().GetKubernetes().GetProfile(),
	)
}

func TestDeployKustomizeRequiresExplicitOutputProfile(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	builder := &BuilderWrapper{Base: &Base{Wool: wool.Get(ctx), loaded: true}}

	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{Destination: t.TempDir()},
		}},
	}, KustomizeDeployment{EnvironmentVariables: manager, Templates: templates})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "output profile is required")
}

func TestDeployKustomizeReturnsValidationEvidenceOnFailure(t *testing.T) {
	ctx := context.Background()
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:        wool.Get(ctx),
		Identity:    identity,
		Information: &Information{Service: resources.ToServiceWithCase(identity)},
		loaded:      true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	templates := fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl": {
			Data: []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - secret.yaml\n"),
		},
		"templates/deployment/kustomize/base/secret.yaml.tmpl": {
			Data: []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: unsafe\n  namespace: codefly\ndata:\n  token: dW5zYWZl\n"),
		},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {
			Data: []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../../base\n"),
		},
	}

	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: t.TempDir(),
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			},
		}},
	}, KustomizeDeployment{EnvironmentVariables: manager, Templates: templates})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	output := response.GetDeployment().GetKubernetes()
	require.Equal(t, KubernetesManifestContractVersion, output.GetContractVersion())
	require.Equal(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1, output.GetProfile())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_FAILED, output.GetValidation().GetStaticValidation())
	require.Contains(t, strings.Join(output.GetValidation().GetViolations(), "\n"), "emits a Secret resource")
}

func TestDeployKustomizeRejectsMissingDependencies(t *testing.T) {
	builder := &BuilderWrapper{}
	response, err := builder.DeployKustomize(context.Background(), nil, KustomizeDeployment{})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "environment variable manager")
}

func TestDeployKustomizeDoesNotReuseOutputOnEarlyError(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder

	success, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: t.TempDir(),
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "ephemeral"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, success.GetState().GetState())
	require.NotNil(t, success.GetDeployment())

	failure, err := builder.DeployKustomize(ctx, nil, KustomizeDeployment{EnvironmentVariables: manager})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, failure.GetState().GetState())
	require.Contains(t, failure.GetState().GetMessage(), "requires templates")
	require.Nil(t, failure.GetDeployment())
}

func TestApplicationDeploymentInputs(t *testing.T) {
	inputs := ApplicationDeploymentInputs()
	require.True(t, inputs.OwnEndpoints)
	require.True(t, inputs.DependencyEndpoints)
	require.True(t, inputs.OwnConfiguration)
	require.True(t, inputs.DependencyConfigurations)
}

func configuration(origin, name, key, value string, secret bool) *basev0.Configuration {
	return &basev0.Configuration{
		Origin: origin,
		Infos: []*basev0.ConfigurationInformation{{
			Name: name,
			ConfigurationValues: []*basev0.ConfigurationValue{{
				Key: key, Value: value, Secret: secret,
			}},
		}},
	}
}
