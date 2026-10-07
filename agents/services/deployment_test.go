package services

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
		Location:             agentWorkspace(t, "module", "saas"),
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		Service: &resources.Service{ServiceDependencies: []*resources.ServiceDependency{{
			Module:    "saas",
			Name:      "accounts",
			Endpoints: []*resources.EndpointReference{{Name: "usage"}},
		}}},
		loaded: true,
	}
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder

	destination := t.TempDir()
	req := &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test", Fixture: "dev-admin"},
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
		DependenciesNetworkMappings: []*basev0.NetworkMapping{
			dependencyMapping("saas", "accounts", "grpc", "grpc", 9090, resources.VisibilityPrivate),
			dependencyMapping("saas", "accounts", "usage", "grpc", 19090, resources.VisibilityPublic),
		},
	}

	response, err := builder.DeployKustomize(ctx, req, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs: DeploymentInputs{
			OwnConfiguration:         true,
			DependencyConfigurations: true,
			DependencyEndpoints:      true,
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
	require.False(t, response.GetDeployment().GetKubernetes().GetValidation().GetRestricted())

	configMapManifest, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	manifest := string(configMapManifest)
	for _, expected := range []string{
		`CODEFLY__RUNNING: "true"`,
		`CODEFLY__ENVIRONMENT: "test"`,
		`CODEFLY__FIXTURE: "dev-admin"`,
		`CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__APPLICATION__PLAIN: "value"`,
		`CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__CONNECTION__URL: "redis://service"`,
		`CODEFLY__ENDPOINT__SAAS__ACCOUNTS__USAGE__GRPC: "accounts:19090"`,
		`EXTRA: "config"`,
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("ConfigMap missing %q:\n%s", expected, manifest)
		}
	}
	require.NotContains(t, manifest, "CODEFLY__ENDPOINT__SAAS__ACCOUNTS__GRPC__GRPC")

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

func dependencyMapping(module, service, name, api string, port uint16, visibility resources.Visibility) *basev0.NetworkMapping {
	instance := resources.NewNetworkInstance(service, port)
	instance.Access = resources.NewContainerNetworkAccess()
	return &basev0.NetworkMapping{
		Endpoint:  &basev0.Endpoint{Module: module, Service: service, Name: name, Api: api, Visibility: visibility, Exposure: exposureFor(visibility)},
		Instances: []*basev0.NetworkInstance{instance},
	}
}

func TestDeployKustomizeRejectsSecretBytesForRestricted(t *testing.T) {
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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

func TestDeployKustomizeRejectsSecretConfigurationDataForRestricted(t *testing.T) {
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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

func TestDeployKustomizeRendersRestrictedSecretFreeTreeWithoutClusterAccess(t *testing.T) {
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	output := response.GetDeployment().GetKubernetes()
	require.Equal(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, output.GetProfile())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, output.GetValidation().GetStaticValidation())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_NOT_RUN, output.GetValidation().GetServerSideValidation())
	require.True(t, output.GetValidation().GetRestricted())
	_, err = os.Stat(filepath.Join(destination, "base", "secret.yaml"))
	require.True(t, os.IsNotExist(err), "restricted render must omit the empty secret manifest, not leave an empty stub")
	deploymentManifest, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(deploymentManifest), "must-not-render")
	require.Contains(t, string(deploymentManifest), "name: service-secrets")
	require.Contains(t, string(deploymentManifest), "key: database-password")
	configMapManifest, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(configMapManifest), `CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__CONNECTION__URL: "redis://service"`)
}

func TestServerSideValidationRequiresExplicitClusterTarget(t *testing.T) {
	err := validateKubernetesManifestServerSide(
		context.Background(),
		nil,
		"codefly",
		"",
		"k3d-codefly",
	)
	require.EqualError(t, err, "server-side validation requires an explicit kubeconfig")

	err = validateKubernetesManifestServerSide(
		context.Background(),
		nil,
		"codefly",
		"/tmp/kubeconfig",
		"",
	)
	require.EqualError(t, err, "server-side validation requires an explicit Kubernetes context")
}

func TestServerSideValidationUsesIsolatedResourceNames(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: store
  namespace: platform
spec:
  serviceName: store
---
apiVersion: batch/v1
kind: Job
metadata:
  name: seed
  namespace: platform
spec:
  template:
    spec:
      containers:
        - name: seed
          env:
            - name: TOKEN
              valueFrom:
                secretKeyRef:
                  name: store
                  key: token
`)

	isolated, err := isolateServerSideValidationResources(manifest, "12345678")
	require.NoError(t, err)
	require.Contains(t, string(isolated), "name: store-codefly-validation-12345678")
	require.Contains(t, string(isolated), "name: seed-codefly-validation-12345678")
	require.Contains(t, string(isolated), "serviceName: store")
	require.Regexp(t, `secretKeyRef:\n\s+key: token\n\s+name: store`, string(isolated))

	longName := strings.Repeat("a", 63)
	require.Len(t, isolatedValidationResourceName(longName, "12345678"), 63)
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, gitOpsResponse.GetState().GetState())
	require.NotContains(t, gitOpsResponse.GetState().GetMessage(), "cannot receive secret values")
	_, err = os.Stat(filepath.Join(gitOpsDestination, "base", "secret.yaml"))
	require.True(t, os.IsNotExist(err), "restricted render must omit the empty secret manifest, not leave an empty stub")
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
					Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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

// Deleting the PROMOTABLE_GITOPS_V1 value deleted its name, not its number:
// a request carrying 2 still decodes, from the wire and from JSON, so the
// decoder is not the gate. Compared raw, 2 is merely "not restricted", and the
// public renderer would emit base/secret.yaml with the inline TOKEN for it —
// the opposite of what the profile meant. Every rendering entrypoint judges the
// profile first and refuses the number before anything is written.
func TestARenderRefusesADecodedDeletedProfileBeforeWriting(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)

	// The number survives the wire decoder.
	encoded, err := proto.Marshal(&builderv0.KubernetesDeployment{
		Namespace: "codefly", Profile: builderv0.KubernetesOutputProfile(2),
	})
	require.NoError(t, err)
	var decoded builderv0.KubernetesDeployment
	require.NoError(t, proto.Unmarshal(encoded, &decoded))
	require.Equal(t, builderv0.KubernetesOutputProfile(2), decoded.GetProfile())

	// It survives the JSON decoder too, where only the deleted NAME is refused.
	var fromJSON builderv0.KubernetesDeployment
	require.NoError(t, protojson.Unmarshal([]byte(`{"namespace":"codefly","profile":2}`), &fromJSON))
	require.Equal(t, builderv0.KubernetesOutputProfile(2), fromJSON.GetProfile())
	require.Error(t, protojson.Unmarshal([]byte(`{"profile":"KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1"}`), &builderv0.KubernetesDeployment{}))

	builder, manager := restrictedDeployBuilder(ctx, t)
	secrets := DeploymentParameters{
		ConfigMap: EnvironmentMap{"PLAIN": "value"},
		SecretMap: EnvironmentMap{"TOKEN": "c2VjcmV0"},
	}
	for _, profile := range []builderv0.KubernetesOutputProfile{2, 7} {
		// The direct renderer: refused by the judgement, nothing written.
		destination := t.TempDir()
		decoded.Destination = destination
		decoded.Profile = profile
		err = builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, &decoded, templates, secrets)
		require.ErrorIs(t, err, ErrOutputProfileUnknown, "profile %d", profile)
		entries, readErr := os.ReadDir(destination)
		require.NoError(t, readErr)
		require.Empty(t, entries, "profile %d: the destination must be untouched", profile)

		// The write itself, handed a base that claims an unrestricted render:
		// refused before the destination is emptied.
		base, baseErr := builder.CreateKubernetesBase(ctx, &basev0.Environment{Name: "test"}, "codefly", nil)
		require.NoError(t, baseErr)
		base.Profile, base.Restricted = profile, false
		marker := filepath.Join(destination, "untouched")
		require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
		err = builder.GenerateGenericKustomize(ctx, templates, &decoded, base, secrets)
		require.ErrorIs(t, err, ErrOutputProfileUnknown, "profile %d", profile)
		_, statErr := os.Stat(marker)
		require.NoError(t, statErr, "profile %d: the destination must not have been emptied", profile)

		// The request pre-flight a plugin's own Deploy runs: refused by the
		// same judgement, before any input is collected.
		destination = t.TempDir()
		request := &builderv0.DeploymentRequest{
			Environment: &basev0.Environment{Name: "test"},
			Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
				Kubernetes: &builderv0.KubernetesDeployment{Namespace: "codefly", Destination: destination, Profile: profile},
			}},
		}
		_, err = builder.KubernetesDeploymentRequest(ctx, request)
		require.ErrorIs(t, err, ErrOutputProfileUnknown, "profile %d", profile)

		// The full deployment: an error response naming the refusal, nothing written.
		response, deployErr := builder.DeployKustomize(ctx, request,
			KustomizeDeployment{EnvironmentVariables: manager, Templates: templates, Parameters: struct{ Name string }{Name: "deleted"}})
		require.NoError(t, deployErr)
		require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
		require.Contains(t, response.GetState().GetMessage(), "output profile")
		require.Contains(t, response.GetState().GetMessage(), "not one this contract defines")
		entries, readErr = os.ReadDir(destination)
		require.NoError(t, readErr)
		require.Empty(t, entries, "profile %d: the destination must be untouched", profile)
	}

	// The judgement of a rendered tree and its bundle refuse the number too,
	// rather than describing the tree as an unrestricted render.
	validation := ValidateKubernetesManifestTree(ctx, t.TempDir(), "test", "codefly", builderv0.KubernetesOutputProfile(2), false, "", "")
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_FAILED, validation.GetStaticValidation())
	require.False(t, validation.GetRestricted())
	require.Len(t, validation.GetViolations(), 1)
	require.Contains(t, validation.GetViolations()[0], "unsupported Kubernetes output profile")
	require.Contains(t, validation.GetViolations()[0], "number 2 names a value the schema deleted")
	_, err = BuildKubernetesManifestBundle(t.TempDir(), "test", builderv0.KubernetesOutputProfile(2), validation, nil)
	require.ErrorIs(t, err, ErrOutputProfileUnknown)

	// A base that disagrees with a VALID request's profile is refused the same
	// way: the write holds the judgement, not the caller's assembly.
	destination := t.TempDir()
	valid := &builderv0.KubernetesDeployment{Namespace: "codefly", Destination: destination,
		Profile: builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1}
	base, err := builder.CreateKubernetesBase(ctx, &basev0.Environment{Name: "test"}, "codefly", nil)
	require.NoError(t, err)
	base.Profile, base.Restricted = valid.GetProfile(), false
	err = builder.GenerateGenericKustomize(ctx, templates, valid, base, secrets)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not carry the request's judged profile")
	entries, err := os.ReadDir(destination)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// The judgement comes first: a refused profile stops KustomizeDeploy before
// the deployment base is built, so nothing of the render — not even the image
// ID resolution the base performs — runs for a request that will not be
// rendered. The CLI-injected ImageIDResolver is the observable step of
// CreateKubernetesBase, so it is the witness.
func TestKustomizeDeployJudgesTheProfileBeforeBuildingTheBase(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, _ := restrictedDeployBuilder(ctx, t)

	previous := ImageIDResolver
	t.Cleanup(func() { ImageIDResolver = previous })
	resolved := 0
	ImageIDResolver = func(*resources.DockerImage) (string, error) {
		resolved++
		return "sha256:" + strings.Repeat("b", 64), nil
	}

	err = builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, &builderv0.KubernetesDeployment{
		Namespace: "codefly", Destination: t.TempDir(), Profile: builderv0.KubernetesOutputProfile(2),
	}, templates, DeploymentParameters{ConfigMap: EnvironmentMap{"PLAIN": "value"}, Parameters: struct{ Name string }{Name: "judged"}})
	require.ErrorIs(t, err, ErrOutputProfileUnknown)
	require.Equal(t, 0, resolved, "a refused profile must stop the render before the base is built")

	// The same hook is on the path of a render that is judged fit.
	require.NoError(t, builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, &builderv0.KubernetesDeployment{
		Namespace: "codefly", Destination: t.TempDir(), Profile: builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
		SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{"DATABASE_PASSWORD": {Name: "service-secrets", Key: "database-password"}},
	}, templates, DeploymentParameters{ConfigMap: EnvironmentMap{"PLAIN": "value"}, Parameters: struct{ Name string }{Name: "judged"},
		SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{"DATABASE_PASSWORD": {Name: "service-secrets", Key: "database-password"}}}))
	require.Equal(t, 1, resolved)
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
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
			},
		}},
	}, KustomizeDeployment{EnvironmentVariables: manager, Templates: templates})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	output := response.GetDeployment().GetKubernetes()
	require.Equal(t, KubernetesManifestContractVersion, output.GetContractVersion())
	require.Equal(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, output.GetProfile())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_FAILED, output.GetValidation().GetStaticValidation())
	require.Contains(t, strings.Join(output.GetValidation().GetViolations(), "\n"), "emits a Secret resource")
	require.Contains(t, response.GetState().GetMessage(), "emits a Secret resource")
	require.Nil(t, output.GetBundle(), "a failed deployment must not emit a deliverable manifest bundle")
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

func restrictedDeployBuilder(ctx context.Context, t *testing.T) (*BuilderWrapper, *resources.EnvironmentVariableManager) {
	t.Helper()
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
	return builder, manager
}

func renderRestricted(ctx context.Context, t *testing.T, profile builderv0.KubernetesOutputProfile) *builderv0.KubernetesDeploymentOutput {
	t.Helper()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, manager := restrictedDeployBuilder(ctx, t)
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: t.TempDir(),
				Profile:     profile,
				SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{
					"DATABASE_PASSWORD": {Name: "service-secrets", Key: "database-password"},
				},
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "restricted"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	return response.GetDeployment().GetKubernetes()
}

func TestDeployKustomizeEmitsDeterministicManifestBundle(t *testing.T) {
	ctx := context.Background()
	output := renderRestricted(ctx, t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1)
	bundle := output.GetBundle()
	require.NotNil(t, bundle)
	require.Equal(t, builderv0.KubernetesDeploymentOutput_KUSTOMIZE, bundle.GetFormat())
	require.Equal(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, bundle.GetProfile())
	require.Equal(t, KubernetesManifestContractVersion, bundle.GetContractVersion())
	require.Equal(t, []string{"overlays/test"}, bundle.GetEntryPoints())
	require.NotEmpty(t, bundle.GetFiles())
	require.True(t, bundle.GetValidation().GetRestricted())
	require.Contains(t, bundle.GetSecretReferences(), "DATABASE_PASSWORD")

	paths := make([]string, 0, len(bundle.GetFiles()))
	for _, file := range bundle.GetFiles() {
		require.True(t, strings.HasPrefix(file.GetDigest(), "sha256:"), "file %s digest %q", file.GetPath(), file.GetDigest())
		require.NotContains(t, file.GetPath(), `\`, "file inventory must use POSIX paths")
		paths = append(paths, file.GetPath())
	}
	require.True(t, sort.StringsAreSorted(paths), "file inventory must be sorted: %v", paths)
	require.Contains(t, paths, "base/deployment.yaml")
	require.True(t, strings.HasPrefix(bundle.GetDigest(), "sha256:"))

	// The same inputs rendered into a different destination yield an identical
	// bundle digest: the bundle is a deterministic function of the inputs, not
	// of the caller-supplied destination path.
	again := renderRestricted(ctx, t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1)
	require.Equal(t, bundle.GetDigest(), again.GetBundle().GetDigest())
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

// A deploy hands the consumer its dependencies' addresses just as a run does,
// and the builder agent judges them with the composition its service was
// loaded from — the workspace above the service directory — exactly as the
// CLI-side wrapper that handed them did: a private endpoint of another module
// is refused (the incident this guards: a workspace that failed static
// validation, failed to run, and still deployed, shipping the private
// endpoint's address to the cluster), a solution's route to a module is
// refused, an agent loaded outside any workspace refuses the addresses as
// unjudged, and what the dependencies do not name is not wired.
func TestDeployKustomizeJudgesWithTheWorkspaceItWasLoadedFrom(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)

	deploy := func(t *testing.T, location string, mappings ...*basev0.NetworkMapping) (*builderv0.DeploymentResponse, string) {
		t.Helper()
		manager := resources.NewEnvironmentVariableManager()
		manager.SetIdentity(&basev0.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"})
		identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
		base := &Base{
			Wool:                 wool.Get(ctx),
			Identity:             identity,
			Location:             location,
			Information:          &Information{Service: resources.ToServiceWithCase(identity)},
			EnvironmentVariables: manager,
			Service: &resources.Service{ServiceDependencies: []*resources.ServiceDependency{{
				Module:    "saas",
				Name:      "accounts",
				Kind:      resources.DependencyKindRuntime,
				Endpoints: []*resources.EndpointReference{{Name: "usage"}},
			}}},
			loaded: true,
		}
		base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
		builder := &BuilderWrapper{Base: base}
		base.Builder = builder
		destination := t.TempDir()
		req := &builderv0.DeploymentRequest{
			Environment: &basev0.Environment{Name: "test", Fixture: "dev-admin"},
			Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
				Kubernetes: &builderv0.KubernetesDeployment{
					Namespace:   "codefly",
					Destination: destination,
					Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
				},
			}},
			DependenciesNetworkMappings: mappings,
		}
		response, err := builder.DeployKustomize(ctx, req, KustomizeDeployment{
			EnvironmentVariables: manager,
			Templates:            templates,
			Inputs:               DeploymentInputs{DependencyEndpoints: true},
			Parameters:           struct{ Name string }{Name: "prepared"},
		})
		require.NoError(t, err, "a refusal is reported as a failed deployment, not a transport error")
		manifest := ""
		if raw, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml")); err == nil {
			manifest = string(raw)
		}
		return response, manifest
	}
	usage := func(visibility string) *basev0.NetworkMapping {
		return dependencyMapping("saas", "accounts", "usage", "grpc", 19090, visibility)
	}

	t.Run("a private endpoint of another module is refused", func(t *testing.T) {
		response, _ := deploy(t, agentWorkspace(t, "module", "saas"), usage(resources.VisibilityPrivate))
		require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
		require.Contains(t, response.GetState().GetMessage(), `private to module "saas"`)
	})
	t.Run("a permitted endpoint is wired, and only what the dependency names", func(t *testing.T) {
		response, manifest := deploy(t, agentWorkspace(t, "module", "saas"),
			usage(resources.VisibilityInternal),
			dependencyMapping("saas", "accounts", "admin", "grpc", 19091, resources.VisibilityInternal))
		require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
		require.Contains(t, manifest, `CODEFLY__ENDPOINT__SAAS__ACCOUNTS__USAGE__GRPC: "accounts:19090"`)
		require.NotContains(t, manifest, "CODEFLY__ENDPOINT__SAAS__ACCOUNTS__ADMIN__GRPC")
	})
	t.Run("a solution's route to a module is refused", func(t *testing.T) {
		response, _ := deploy(t, solutionWorkspace(t), usage(resources.VisibilityInternal))
		require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
		require.Contains(t, response.GetState().GetMessage(), resources.ErrSolutionReachesThroughHost.Error())
	})
	t.Run("an agent loaded outside any workspace refuses the addresses as unjudged", func(t *testing.T) {
		response, _ := deploy(t, t.TempDir(), usage(resources.VisibilityInternal))
		require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
		require.Contains(t, response.GetState().GetMessage(), resources.ErrUnjudgedProvenance.Error())
	})
}

// exposureFor states the exposure a public endpoint never omits.
func exposureFor(visibility resources.Visibility) string {
	if visibility == resources.VisibilityPublic {
		return resources.ExposureNone
	}
	return ""
}

// agentWorkspace writes a modules-layout workspace carrying the modules the
// deploy tests name, and returns the service directory the agent is loaded
// from — the composition the agent judges dependency addresses with.
func agentWorkspace(t *testing.T, modules ...string) string {
	t.Helper()
	root := t.TempDir()
	manifest := "name: workspace\nlayout: modules\nmodules:\n"
	for _, module := range modules {
		manifest += "  - name: " + module + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.codefly.yaml"), []byte(manifest), 0o600))
	service := filepath.Join(root, "modules", "module", "services", "service")
	require.NoError(t, os.MkdirAll(service, 0o755))
	return service
}

// solutionWorkspace writes a product composing a platform workspace (module
// saas) and declaring the agent's module as a SOLUTION, and returns the
// service directory the agent is loaded from.
func solutionWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		file := filepath.Join(root, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	}
	write("core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: saas\n")
	write("core/modules/saas/module.codefly.yaml", "kind: module\nname: saas\nservices: []\n")
	write("product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\nsolutions:\n  - name: module\n    path: solutions/module\n")
	write("product/solutions/module/module.codefly.yaml", "kind: module\nname: module\nservices: []\n")
	service := filepath.Join(root, "product", "solutions", "module", "services", "service")
	require.NoError(t, os.MkdirAll(service, 0o755))
	return service
}
