package services

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestPodTemplateOverlayValidateConfigMounts(t *testing.T) {
	tests := []struct {
		name    string
		mounts  []ConfigMount
		wantErr string
	}{
		{name: "valid", mounts: []ConfigMount{{ConfigMapName: "skin-config", MountPath: "/etc/skin", VolumeName: "skin-config"}}},
		{name: "relative path", mounts: []ConfigMount{{ConfigMapName: "skin", MountPath: "etc/skin"}}, wantErr: "must be absolute"},
		{name: "bad configmap name", mounts: []ConfigMount{{ConfigMapName: "Skin_Config", MountPath: "/etc/skin"}}, wantErr: "DNS-1123 subdomain"},
		{name: "empty configmap name", mounts: []ConfigMount{{MountPath: "/etc/skin"}}, wantErr: "DNS-1123 subdomain"},
		{
			name: "duplicate path",
			mounts: []ConfigMount{
				{ConfigMapName: "a", MountPath: "/etc/x"},
				{ConfigMapName: "b", MountPath: "/etc/x"},
			},
			wantErr: "mounted more than once",
		},
		{name: "bad volume name", mounts: []ConfigMount{{ConfigMapName: "skin", MountPath: "/etc/skin", VolumeName: "Bad_Vol"}}, wantErr: "DNS-1123 label"},
		{
			name: "duplicate volume name",
			mounts: []ConfigMount{
				{ConfigMapName: "a", MountPath: "/etc/a", VolumeName: "shared"},
				{ConfigMapName: "b", MountPath: "/etc/b", VolumeName: "shared"},
			},
			wantErr: "used more than once",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&PodTemplateOverlay{ConfigMounts: tc.mounts}).Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestDefaultConfigMounts(t *testing.T) {
	writable := false
	overlay := &PodTemplateOverlay{ConfigMounts: []ConfigMount{
		{ConfigMapName: "app.skin.config", MountPath: "/etc/skin"},
		{ConfigMapName: "app.skin.config", MountPath: "/etc/skin-alt"},
		{ConfigMapName: "theme", MountPath: "/etc/theme", VolumeName: "custom-vol", ReadOnly: &writable},
	}}
	overlay.DefaultConfigMounts()

	// Dots in a ConfigMap name become dashes so the derived volume name is a
	// DNS-1123 label, and a collision gets a stable index suffix.
	require.Equal(t, "app-skin-config", overlay.ConfigMounts[0].VolumeName)
	require.Equal(t, "app-skin-config-2", overlay.ConfigMounts[1].VolumeName)
	// A caller-supplied volume name is left untouched.
	require.Equal(t, "custom-vol", overlay.ConfigMounts[2].VolumeName)
	// ReadOnly defaults to true when unset...
	require.True(t, *overlay.ConfigMounts[0].ReadOnly)
	require.True(t, *overlay.ConfigMounts[1].ReadOnly)
	// ...but an explicit value is honored, not overwritten.
	require.False(t, *overlay.ConfigMounts[2].ReadOnly)
	require.True(t, overlay.HasConfigMounts())
	require.NoError(t, overlay.Validate())
}

// A caller-supplied VolumeName that collides with an earlier derived name is a
// duplicate pod volume — DefaultConfigMounts must not silently rename the
// caller's explicit name, so Validate has to reject the collision.
func TestDefaultConfigMountsRejectsExplicitVolumeCollision(t *testing.T) {
	overlay := &PodTemplateOverlay{ConfigMounts: []ConfigMount{
		{ConfigMapName: "foo", MountPath: "/etc/foo"},                    // derives "foo"
		{ConfigMapName: "bar", MountPath: "/etc/bar", VolumeName: "foo"}, // explicit, collides
	}}
	overlay.DefaultConfigMounts()
	require.Equal(t, "foo", overlay.ConfigMounts[0].VolumeName)
	require.Equal(t, "foo", overlay.ConfigMounts[1].VolumeName)
	require.ErrorContains(t, overlay.Validate(), "used more than once")
}

func TestHasConfigMounts(t *testing.T) {
	require.False(t, (*PodTemplateOverlay)(nil).HasConfigMounts())
	require.False(t, (&PodTemplateOverlay{}).HasConfigMounts())
	require.True(t, (&PodTemplateOverlay{ConfigMounts: []ConfigMount{{ConfigMapName: "a", MountPath: "/etc/a"}}}).HasConfigMounts())
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
			manifest := string(deployment)
			require.NotContains(t, manifest, "serviceAccountName:")
			require.NotContains(t, manifest, "volumeMounts:", "no config mounts means no volume rendering")
			require.NotContains(t, manifest, "volumes:")
		})
	}
}

func TestDeployKustomizeRendersConfigMounts(t *testing.T) {
	ctx := context.Background()
	writable := false
	destination := deployWithOverlay(ctx, t, &PodTemplateOverlay{
		ConfigMounts: []ConfigMount{
			{
				ConfigMapName: "skin-config",
				MountPath:     "/etc/codefly/skin",
				Optional:      true,
			},
			{
				ConfigMapName: "theme-config",
				MountPath:     "/etc/codefly/theme",
				ReadOnly:      &writable,
			},
		},
	})

	deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	manifest := string(deployment)
	require.Contains(t, manifest, "volumeMounts:")
	require.Contains(t, manifest, "mountPath: /etc/codefly/skin")
	// VolumeName was derived from the ConfigMap name and shared by the mount and
	// the volume, and ReadOnly defaulted to true.
	require.Contains(t, manifest, "name: skin-config")
	require.Contains(t, manifest, "readOnly: true")
	require.Contains(t, manifest, "configMap:")
	require.Contains(t, manifest, "optional: true")
	// An explicit ReadOnly is honored rather than forced to true.
	require.Contains(t, manifest, "mountPath: /etc/codefly/theme")
	require.Contains(t, manifest, "readOnly: false")
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

func TestDeployKustomizeDefaultsServiceAccountNameToService(t *testing.T) {
	ctx := context.Background()
	// An overlay that opts into an SA but leaves the name empty is keyed off the
	// service name, so a module opts in without hardcoding a per-service string.
	destination := deployWithOverlay(ctx, t, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{
			Annotations: map[string]string{"azure.workload.identity/client-id": "00000000-0000-0000-0000-000000000000"},
		},
	})

	deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(deployment), "serviceAccountName: service")

	sa, err := os.ReadFile(filepath.Join(destination, "base", "serviceaccount.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(sa), "name: service")
	require.Contains(t, string(sa), "azure.workload.identity/client-id: 00000000-0000-0000-0000-000000000000")
}

func TestApplyPodOverlayReportsRenderedConfigVolumes(t *testing.T) {
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
    spec:
      containers:
        - name: web
          image: example/web
          volumeMounts:
            - name: skin-config
              mountPath: /etc/skin
              readOnly: true
      volumes:
        - name: skin-config
          configMap:
            name: skin-config
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte(manifest), 0o644))

	overlay := &PodTemplateOverlay{ConfigMounts: []ConfigMount{{ConfigMapName: "skin-config", MountPath: "/etc/skin", VolumeName: "skin-config"}}}
	result, err := applyPodOverlay(context.Background(), dir, overlay)
	require.NoError(t, err)
	require.True(t, result.renderedConfigVolumes["skin-config"], "a rendered mount's volume must be reported")
	// Config mounts render via the agent template, not this pass — the manifest
	// is left byte-identical.
	after, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Equal(t, manifest, string(after))
}

// TestApplyPodOverlayDetectsUnrenderedConfigMount is the guard behind the
// deploy-time warning: a workload whose template forgot the volumes block leaves
// the config file silently absent, and the result must make that detectable.
func TestApplyPodOverlayDetectsUnrenderedConfigMount(t *testing.T) {
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
    spec:
      containers:
        - name: web
          image: example/web
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte(manifest), 0o644))

	overlay := &PodTemplateOverlay{ConfigMounts: []ConfigMount{{ConfigMapName: "skin-config", MountPath: "/etc/skin", VolumeName: "skin-config"}}}
	result, err := applyPodOverlay(context.Background(), dir, overlay)
	require.NoError(t, err)
	require.False(t, result.renderedConfigVolumes["skin-config"], "an unrendered mount must be detectable")
}

// TestPodOverlayTemplateContractRenders guards the template-facing PodOverlay
// methods (HasServiceAccount, ServiceAccountName) and fields (PodLabels,
// PodAnnotations) that agents such as service-go-grpc still read directly from
// their own deployment templates. A rename or behavior change there compiles
// fine but silently breaks those templates, so we render the contract through
// the real template engine.
func TestPodOverlayTemplateContractRenders(t *testing.T) {
	wrapper := &DeploymentWrapper{
		PodOverlay: &PodTemplateOverlay{
			ServiceAccount: &WorkloadServiceAccount{Name: "db-reader"},
			PodLabels:      map[string]string{"azure.workload.identity/use": "true"},
			PodAnnotations: map[string]string{"codefly.dev/identity": "workload"},
		},
	}
	tmpl := `{{- with .PodOverlay }}
{{- if .HasServiceAccount }}
serviceAccountName: {{ .ServiceAccountName }}
{{- end }}
{{- range $key, $value := .PodLabels }}
{{ $key }}: {{ $value | quote }}
{{- end }}
{{- range $key, $value := .PodAnnotations }}
{{ $key }}: {{ $value | quote }}
{{- end }}
{{- end }}`
	out, err := templates.ApplyTemplate(tmpl, wrapper)
	require.NoError(t, err)
	require.Contains(t, out, "serviceAccountName: db-reader")
	require.Contains(t, out, `azure.workload.identity/use: "true"`)
	require.Contains(t, out, `codefly.dev/identity: "workload"`)
}

// TestConfigMountTemplateContractRenders guards the template-facing ConfigMount
// contract (HasConfigMounts + the ConfigMount fields) that consuming agents such
// as service-nextjs render from their own deployment templates.
func TestConfigMountTemplateContractRenders(t *testing.T) {
	overlay := &PodTemplateOverlay{ConfigMounts: []ConfigMount{
		{ConfigMapName: "skin-config", MountPath: "/etc/codefly/skin", Optional: true},
	}}
	overlay.DefaultConfigMounts()
	wrapper := &DeploymentWrapper{PodOverlay: overlay}
	tmpl := `{{- with .PodOverlay }}{{ if .HasConfigMounts }}
volumeMounts:
{{- range .ConfigMounts }}
  - name: {{ .VolumeName }}
    mountPath: {{ .MountPath }}
    readOnly: {{ .ReadOnly }}
{{- end }}
volumes:
{{- range .ConfigMounts }}
  - name: {{ .VolumeName }}
    configMap:
      name: {{ .ConfigMapName }}
      optional: {{ .Optional }}
{{- end }}
{{- end }}{{ end }}`
	out, err := templates.ApplyTemplate(tmpl, wrapper)
	require.NoError(t, err)
	require.Contains(t, out, "name: skin-config")
	require.Contains(t, out, "mountPath: /etc/codefly/skin")
	require.Contains(t, out, "readOnly: true")
	require.Contains(t, out, "optional: true")
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

	result, err := applyPodOverlay(context.Background(), dir, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "store"},
		PodLabels:      map[string]string{"azure.workload.identity/use": "true"},
		PodAnnotations: map[string]string{"codefly.dev/identity": "workload"},
	})
	require.NoError(t, err)
	require.True(t, result.boundServiceAccount)

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
	result, err := applyPodOverlay(context.Background(), dir, overlay)
	require.NoError(t, err)
	require.True(t, result.boundServiceAccount)

	rendered, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(rendered)
	require.Equal(t, 1, strings.Count(source, "serviceAccountName: web"), source)
	require.Equal(t, 1, strings.Count(source, "azure.workload.identity/use:"), source)
	// A file that needed no change is left byte-identical.
	require.Equal(t, manifest, source)
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

	result, err := applyPodOverlay(context.Background(), dir, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "web"},
	})
	require.NoError(t, err)
	require.False(t, result.boundServiceAccount, "no workload manifest means the SA never got bound")

	rendered, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, configMap, string(rendered), "a non-workload manifest must be left byte-identical")
}

func TestApplyPodOverlayLeavesEmptyTemplateUnbound(t *testing.T) {
	dir := t.TempDir()
	// A workload whose pod template is absent must not be corrupted, and must
	// report that no service account was bound so the caller can warn.
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: codefly
spec:
  selector:
    matchLabels:
      app: web
`
	path := filepath.Join(dir, "deployment.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o644))

	result, err := applyPodOverlay(context.Background(), dir, &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "web"},
	})
	require.NoError(t, err)
	require.False(t, result.boundServiceAccount)

	rendered, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, manifest, string(rendered), "a workload without a pod template must be left byte-identical")
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

// TestProjectServiceAccountRendersDeclaredAttachment walks the whole seam a
// deployment does: a coordinate's declared identity is projected onto an
// already-rendered tree, which gains a ServiceAccount carrying the platform's
// annotations and a workload bound to it with the platform's labels. Nothing in
// the path interprets the keys, so a coordinate on any platform wires its own identity
// webhook by declaring it.
func TestProjectServiceAccountRendersDeclaredAttachment(t *testing.T) {
	dir := renderedWorkloadTree(t)
	err := ProjectServiceAccount(context.Background(), dir, "lodestar", "store", declaredPodOverlay())
	require.NoError(t, err)

	serviceAccount, err := os.ReadFile(filepath.Join(dir, "serviceaccount.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(serviceAccount), "name: store")
	require.Contains(t, string(serviceAccount), "namespace: lodestar")
	require.Contains(t, string(serviceAccount), "iam.gke.io/gcp-service-account: platform-db@obinh-usc1.iam.gserviceaccount.com")
	require.Contains(t, string(serviceAccount), "app.kubernetes.io/managed-by: codefly")

	rendered, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(rendered), "serviceAccountName: store")
	require.Contains(t, string(rendered), `obin.ai/workload-identity: "true"`)
	require.Contains(t, string(rendered), "image: example/store")

	// The ServiceAccount is wired into the kustomization of the directory it was
	// written to, so a tree whose overlay includes that base picks it up.
	kustomization, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(kustomization), "serviceaccount.yaml")
}

// A stamp that binds no workload leaves the pods on the namespace default, where
// token minting has no identity — a deploy that succeeds and a connection that
// fails. The projection must refuse rather than return quietly, because a caller
// post-processing a rendered tree cannot detect it afterwards.
func TestProjectServiceAccountRefusesWhenNothingBound(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yaml"), []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: store\n"), 0o644))

	err := ProjectServiceAccount(context.Background(), dir, "lodestar", "store", declaredPodOverlay())
	require.ErrorContains(t, err, "bound no workload")
}

// A caller projects per service without first asking whether the environment
// declares an identity, so an absent one changes nothing and writes nothing.
func TestProjectServiceAccountWithoutIdentityIsNoOp(t *testing.T) {
	dir := renderedWorkloadTree(t)
	before, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)

	require.NoError(t, ProjectServiceAccount(context.Background(), dir, "lodestar", "store", nil))

	after, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	_, err = os.Stat(filepath.Join(dir, "serviceaccount.yaml"))
	require.True(t, os.IsNotExist(err), "no ServiceAccount should be written without a declared identity")
}

// An identity with no name to bind it to would render a ServiceAccount the pods
// never reference.
func TestProjectServiceAccountRequiresAName(t *testing.T) {
	dir := renderedWorkloadTree(t)
	err := ProjectServiceAccount(context.Background(), dir, "lodestar", "", declaredPodOverlay())
	require.ErrorContains(t, err, "requires a name")
}

func TestProjectServiceAccountRejectsConflictsWithoutWriting(t *testing.T) {
	for _, scenario := range []string{"service account", "identity label", "unreferenced workload", "different namespace"} {
		t.Run(scenario, func(t *testing.T) {
			dir := renderedWorkloadTree(t)
			identity := declaredPodOverlay()
			path := filepath.Join(dir, "deployment.yaml")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			switch scenario {
			case "service account":
				data = []byte(strings.Replace(string(data), "      containers:", "      serviceAccountName: other\n      containers:", 1))
			case "identity label":
				data = []byte(strings.Replace(string(data), "        app: store", "        app: store\n        obin.ai/workload-identity: \"false\"", 1))
			case "unreferenced workload":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources: []\n"), 0o644))
			case "different namespace":
				data = []byte(strings.Replace(string(data), "namespace: lodestar", "namespace: elsewhere", 1))
			}
			require.NoError(t, os.WriteFile(path, data, 0o644))
			kustomization, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
			require.NoError(t, err)
			require.Error(t, ProjectServiceAccount(t.Context(), dir, "lodestar", "store", identity))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, after)
			after, err = os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
			require.NoError(t, err)
			require.Equal(t, kustomization, after)
			require.NoFileExists(t, filepath.Join(dir, "serviceaccount.yaml"))
		})
	}
}

func TestProjectServiceAccountPreservesExistingServiceAccount(t *testing.T) {
	dir := renderedWorkloadTree(t)
	path := filepath.Join(dir, "serviceaccount.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: store
  namespace: lodestar
  annotations:
    example.test/owner: retained
imagePullSecrets:
  - name: private-registry
`), 0o644))
	require.NoError(t, ProjectServiceAccount(t.Context(), dir, "lodestar", "store", declaredPodOverlay()))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "private-registry")
	require.Contains(t, string(data), "example.test/owner: retained")
}

func declaredPodOverlay() *PodTemplateOverlay {
	return &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Annotations: map[string]string{"iam.gke.io/gcp-service-account": "platform-db@obinh-usc1.iam.gserviceaccount.com"}},
		PodLabels:      map[string]string{"obin.ai/workload-identity": "true"},
	}
}

// renderedWorkloadTree is a kustomize base as an agent leaves it: a workload and
// the kustomization that lists it.
func renderedWorkloadTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources:\n  - deployment.yaml\n"), 0o644))
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: store
  namespace: lodestar
spec:
  template:
    metadata:
      labels:
        app: store
    spec:
      containers:
        - name: store
          image: example/store
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deployment.yaml"), []byte(manifest), 0o644))
	return dir
}

func TestAttachServiceAccountRejectsConflictingCallerValues(t *testing.T) {
	overlay := &PodTemplateOverlay{
		ServiceAccount: &WorkloadServiceAccount{Name: "chosen", Annotations: map[string]string{"shared": "agent"}},
		PodLabels:      map[string]string{"shared": "agent"},
	}
	err := overlay.AttachServiceAccount(&WorkloadServiceAccount{
		Annotations: map[string]string{"shared": "platform", "added": "platform"},
	}, map[string]string{"shared": "platform", "added": "platform"})
	require.ErrorContains(t, err, "conflicts")
	require.Equal(t, "chosen", overlay.ServiceAccount.Name)
	require.Equal(t, "agent", overlay.ServiceAccount.Annotations["shared"])
	require.NotContains(t, overlay.ServiceAccount.Annotations, "added")
	require.Equal(t, "agent", overlay.PodLabels["shared"])
	require.NotContains(t, overlay.PodLabels, "added")
}

func TestAttachServiceAccountRequiresAnOverlay(t *testing.T) {
	var overlay *PodTemplateOverlay
	require.Error(t, overlay.AttachServiceAccount(declaredPodOverlay().ServiceAccount, declaredPodOverlay().PodLabels))
}

func TestAttachServiceAccountPreservesExplicitName(t *testing.T) {
	overlay := &PodTemplateOverlay{}
	require.NoError(t, overlay.AttachServiceAccount(&WorkloadServiceAccount{Name: "declared"}, nil))
	require.Equal(t, "declared", overlay.ServiceAccount.Name)
	require.NoError(t, overlay.AttachServiceAccount(&WorkloadServiceAccount{Name: "declared"}, nil))
	require.ErrorContains(t, overlay.AttachServiceAccount(&WorkloadServiceAccount{Name: "different"}, nil), "conflicts")
	require.Equal(t, "declared", overlay.ServiceAccount.Name)
}

func TestProjectServiceAccountHandlesPodAndCronJob(t *testing.T) {
	for _, kind := range []string{"Pod", "CronJob"} {
		t.Run(kind, func(t *testing.T) {
			dir := renderedWorkloadTree(t)
			pod := map[string]any{
				"metadata": map[string]any{"labels": map[string]string{"app": "store"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "store", "image": "example/store"}}},
			}
			var workload map[string]any
			if kind == "Pod" {
				workload = pod
				workload["apiVersion"] = "v1"
				workload["metadata"].(map[string]any)["name"] = "store"
				workload["metadata"].(map[string]any)["namespace"] = "lodestar"
			} else {
				workload = map[string]any{
					"apiVersion": "batch/v1",
					"metadata":   map[string]any{"name": "store", "namespace": "lodestar"},
					"spec":       map[string]any{"schedule": "0 * * * *", "jobTemplate": map[string]any{"spec": map[string]any{"template": pod}}},
				}
			}
			workload["kind"] = kind
			data, err := yaml.Marshal(workload)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "deployment.yaml"), data, 0o640))
			require.NoError(t, os.Chmod(filepath.Join(dir, "deployment.yaml"), 0o640))
			require.NoError(t, ProjectServiceAccount(t.Context(), dir, "lodestar", "store", declaredPodOverlay()))
			data, err = os.ReadFile(filepath.Join(dir, "deployment.yaml"))
			require.NoError(t, err)
			var document yaml.Node
			require.NoError(t, yaml.Unmarshal(data, &document))
			template := workloadPodTemplate(document.Content[0])
			require.Equal(t, "store", mappingScalar(mappingChild(template, "spec"), "serviceAccountName"))
			require.Equal(t, "true", mappingScalar(mappingChild(mappingChild(template, "metadata"), "labels"), "obin.ai/workload-identity"))
			info, err := os.Stat(filepath.Join(dir, "deployment.yaml"))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
		})
	}
}

func TestProjectServiceAccountValidatesKustomizeTransformations(t *testing.T) {
	dir := renderedWorkloadTree(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("namePrefix: production-\nnamespace: deployed\nresources:\n  - deployment.yaml\n"), 0o644))
	require.NoError(t, ProjectServiceAccount(t.Context(), dir, "lodestar", "store", declaredPodOverlay()))
}

func TestProjectServiceAccountCancellationWhileLockedPreservesInputs(t *testing.T) {
	dir := renderedWorkloadTree(t)
	before, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	lock := flock.New(filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".identity.lock"))
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ProjectServiceAccount(ctx, dir, "lodestar", "store", declaredPodOverlay()), context.DeadlineExceeded)
	after, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoFileExists(t, filepath.Join(dir, "serviceaccount.yaml"))
}

func TestProjectServiceAccountSerializesConflictingWriters(t *testing.T) {
	dir := renderedWorkloadTree(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, principal := range []string{"first", "second"} {
		go func() {
			<-start
			identity := &PodTemplateOverlay{
				ServiceAccount: &WorkloadServiceAccount{Annotations: map[string]string{"identity.example/principal": principal}},
				PodLabels:      map[string]string{"identity.example/principal": principal},
			}
			results <- ProjectServiceAccount(t.Context(), dir, "lodestar", "store", identity)
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil), "exactly one conflicting identity may win: %v, %v", first, second)
	var account, workload yaml.Node
	data, err := os.ReadFile(filepath.Join(dir, "serviceaccount.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &account))
	data, err = os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &workload))
	annotations := mappingChild(mappingChild(account.Content[0], "metadata"), "annotations")
	labels := mappingChild(mappingChild(workloadPodTemplate(workload.Content[0]), "metadata"), "labels")
	require.Equal(t, mappingScalar(annotations, "identity.example/principal"), mappingScalar(labels, "identity.example/principal"))
}

func TestProjectServiceAccountCancellationAtPublicationIsAtomic(t *testing.T) {
	dir := t.TempDir()
	var kustomization strings.Builder
	kustomization.WriteString("resources:\n")
	const count = 150
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%04d.yaml", i)
		fmt.Fprintf(&kustomization, "- %s\n", name)
		body := fmt.Sprintf("apiVersion: v1\nkind: Pod\nmetadata:\n  name: pod-%d\n  namespace: app\nspec:\n  containers:\n  - name: app\n    image: example:1\n", i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(kustomization.String()), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			data, _ := os.ReadFile(filepath.Join(dir, "0000.yaml"))
			if strings.Contains(string(data), "serviceAccountName:") {
				cancel()
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	err := ProjectServiceAccount(ctx, dir, "app", "identity", declaredPodOverlay())
	cancel()
	<-done
	require.NoError(t, err, "an observed published workload must mean the entire tree committed")
	for i := 0; i < count; i++ {
		name := filepath.Join(dir, fmt.Sprintf("%04d.yaml", i))
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		require.Contains(t, string(data), "serviceAccountName: identity")
		info, err := os.Stat(name)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	require.FileExists(t, filepath.Join(dir, "serviceaccount.yaml"))
	data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "serviceaccount.yaml")
}

func TestWorkloadTreeExchangeFailurePreservesDestination(t *testing.T) {
	dir := renderedWorkloadTree(t)
	before, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Error(t, exchangeWorkloadTree(filepath.Join(t.TempDir(), "absent"), dir))
	after, err := os.ReadFile(filepath.Join(dir, "deployment.yaml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}
