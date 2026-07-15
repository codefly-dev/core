package services

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			Kubernetes: &builderv0.KubernetesDeployment{Namespace: "codefly", Destination: destination},
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

	baseManifest, err := os.ReadFile(filepath.Join(destination, "base", "kustomization.yaml"))
	require.NoError(t, err)
	manifest := string(baseManifest)
	for _, expected := range []string{
		"parameter: prepared",
		"CODEFLY__RUNNING: true",
		"CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__APPLICATION__PLAIN: value",
		"CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__CONNECTION__URL: redis://service",
		"EXTRA: config",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__MODULE__DATABASE__DATABASE__PASSWORD: ZGVwZW5kZW5jeS1zZWNyZXQ=",
		"TOKEN: cmF3LXNlY3JldA==",
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("manifest missing %q:\n%s", expected, manifest)
		}
	}

	overlay, err := os.ReadFile(filepath.Join(destination, "overlays", "test", "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(overlay), "environment: test")
}

func TestDeployKustomizeRejectsMissingDependencies(t *testing.T) {
	builder := &BuilderWrapper{}
	response, err := builder.DeployKustomize(context.Background(), nil, KustomizeDeployment{})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "environment variable manager")
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
