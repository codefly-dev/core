package services

import (
	"context"
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

func TestPodTemplateOverlayValidate(t *testing.T) {
	tests := []struct {
		name    string
		overlay *PodTemplateOverlay
		wantErr string
	}{
		{name: "nil", overlay: nil},
		{name: "no service account", overlay: &PodTemplateOverlay{PodLabels: map[string]string{"a": "b"}}},
		{name: "valid", overlay: &PodTemplateOverlay{ServiceAccount: &WorkloadServiceAccount{Name: "db-reader"}}},
		{
			name:    "annotations without name",
			overlay: &PodTemplateOverlay{ServiceAccount: &WorkloadServiceAccount{Annotations: map[string]string{"x": "y"}}},
			wantErr: "requires a name",
		},
		{
			name:    "invalid dns name",
			overlay: &PodTemplateOverlay{ServiceAccount: &WorkloadServiceAccount{Name: "DB_Reader"}},
			wantErr: "DNS-1123 subdomain",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.overlay.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func podOverlayDeployBuilder(ctx context.Context, t *testing.T) (*BuilderWrapper, *resources.EnvironmentVariableManager) {
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
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	return builder, manager
}

func deployWithOverlay(ctx context.Context, t *testing.T, overlay *PodTemplateOverlay) string {
	t.Helper()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, manager := podOverlayDeployBuilder(ctx, t)
	destination := t.TempDir()
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "overlay"},
		PodOverlay:           overlay,
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED,
		response.GetDeployment().GetKubernetes().GetValidation().GetStaticValidation())
	return destination
}

func TestDeployKustomizeEmitsConformantServiceAccount(t *testing.T) {
	ctx := context.Background()
	destination := deployWithOverlay(ctx, t, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{
			Name:        "db-reader",
			Annotations: map[string]string{"azure.workload.identity/client-id": "00000000-0000-0000-0000-000000000000"},
		},
		PodLabels:      map[string]string{"azure.workload.identity/use": "true"},
		PodAnnotations: map[string]string{"codefly.dev/identity": "workload"},
	})

	sa, err := os.ReadFile(filepath.Join(destination, "base", "serviceaccount.yaml"))
	require.NoError(t, err)
	source := string(sa)
	for _, want := range []string{
		"kind: ServiceAccount",
		"name: db-reader",
		"namespace: codefly",
		"app.kubernetes.io/managed-by: codefly",
		"azure.workload.identity/client-id: 00000000-0000-0000-0000-000000000000",
	} {
		require.Contains(t, source, want)
	}

	kustomization, err := os.ReadFile(filepath.Join(destination, "base", "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(kustomization), "serviceaccount.yaml")

	deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	manifest := string(deployment)
	require.Contains(t, manifest, "serviceAccountName: db-reader")
	require.Contains(t, manifest, `azure.workload.identity/use: "true"`)
	require.Contains(t, manifest, `codefly.dev/identity: "workload"`)
}

func TestDeployKustomizeWithoutOverlayRendersNoServiceAccount(t *testing.T) {
	ctx := context.Background()
	for name, overlay := range map[string]*PodTemplateOverlay{
		"nil":               nil,
		"empty":             {},
		"labels only no sa": {PodLabels: map[string]string{"team": "payments"}},
	} {
		t.Run(name, func(t *testing.T) {
			destination := deployWithOverlay(ctx, t, overlay)
			_, err := os.Stat(filepath.Join(destination, "base", "serviceaccount.yaml"))
			require.True(t, os.IsNotExist(err), "no SA object should render without a service account")
			deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
			require.NoError(t, err)
			require.NotContains(t, string(deployment), "serviceAccountName:")
		})
	}
}

func TestDeployKustomizeRejectsInvalidServiceAccountName(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, manager := podOverlayDeployBuilder(ctx, t)
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
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
		Parameters:           struct{ Name string }{Name: "overlay"},
		PodOverlay:           &PodTemplateOverlay{ServiceAccount: &WorkloadServiceAccount{Name: "Bad_Name"}},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "DNS-1123 subdomain")
}

func TestDeployKustomizePrepareCanDeriveOverlay(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, manager := podOverlayDeployBuilder(ctx, t)
	destination := t.TempDir()
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Parameters:           struct{ Name string }{Name: "overlay"},
		Prepare: func(_ context.Context, deployment *KustomizeDeploymentContext) error {
			deployment.PodOverlay = &PodTemplateOverlay{ServiceAccount: &WorkloadServiceAccount{Name: "derived-sa"}}
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
	deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(deployment), "serviceAccountName: derived-sa")
	require.NoError(t, func() error {
		_, statErr := os.Stat(filepath.Join(destination, "base", "serviceaccount.yaml"))
		return statErr
	}())
}

func TestApplyPodOverlayBindsStatefulSetIdentity(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: store
  namespace: codefly
spec:
  serviceName: store
  template:
    metadata:
      labels:
        app: store
    spec:
      containers:
        - name: store
          image: example/store
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "statefulset.yaml"), []byte(manifest), 0o644))

	err := applyPodOverlay(context.Background(), dir, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "store"},
		PodLabels:      map[string]string{"azure.workload.identity/use": "true"},
		PodAnnotations: map[string]string{"codefly.dev/identity": "workload"},
	})
	require.NoError(t, err)

	rendered, err := os.ReadFile(filepath.Join(dir, "statefulset.yaml"))
	require.NoError(t, err)
	source := string(rendered)
	require.Contains(t, source, "serviceAccountName: store")
	require.Contains(t, source, `azure.workload.identity/use: "true"`)
	require.Contains(t, source, `codefly.dev/identity: "workload"`)
	// The workload's own fields survive the round-trip.
	require.Contains(t, source, "serviceName: store")
	require.Contains(t, source, "image: example/store")
}

func TestApplyPodOverlayIsIdempotentWithRenderedBinding(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: codefly
spec:
  template:
    metadata:
      labels:
        app: web
        azure.workload.identity/use: "true"
    spec:
      serviceAccountName: web
      containers:
        - name: web
          image: example/web
`
	path := filepath.Join(dir, "deployment.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o644))

	overlay := &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "web"},
		PodLabels:      map[string]string{"azure.workload.identity/use": "true"},
	}
	require.NoError(t, applyPodOverlay(context.Background(), dir, overlay))

	rendered, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(rendered)
	require.Equal(t, 1, strings.Count(source, "serviceAccountName: web"), source)
	require.Equal(t, 1, strings.Count(source, "azure.workload.identity/use:"), source)
}

func TestApplyPodOverlaySkipsNonWorkloadManifests(t *testing.T) {
	dir := t.TempDir()
	configMap := `apiVersion: v1
kind: ConfigMap
metadata:
  name: web
  namespace: codefly
data:
  KEY: value
`
	path := filepath.Join(dir, "config-map.yaml")
	require.NoError(t, os.WriteFile(path, []byte(configMap), 0o644))

	require.NoError(t, applyPodOverlay(context.Background(), dir, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "web"},
	}))

	rendered, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, configMap, string(rendered), "a non-workload manifest must be left byte-identical")
}

func TestAddKustomizeResourceIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	require.NoError(t, os.WriteFile(path, []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n"), 0o644))
	require.NoError(t, addKustomizeResource(path, "serviceaccount.yaml"))
	require.NoError(t, addKustomizeResource(path, "serviceaccount.yaml"))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(content), "serviceaccount.yaml"))
}
