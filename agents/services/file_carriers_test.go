package services

import (
	"context"
	"fmt"
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
	"gopkg.in/yaml.v3"
)

func renderWithConfiguration(t *testing.T, profile builderv0.KubernetesOutputProfile, configurations ...*basev0.Configuration) (string, *builderv0.DeploymentResponse, error) {
	t.Helper()
	return renderPrepared(t, profile, nil, configurations...)
}

func renderPrepared(t *testing.T, profile builderv0.KubernetesOutputProfile, prepare func(context.Context, *KustomizeDeploymentContext) error, configurations ...*basev0.Configuration) (string, *builderv0.DeploymentResponse, error) {
	t.Helper()
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "runtime", Name: "worker", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{Name: "example/worker", Digest: "sha256:" + strings.Repeat("a", 64)})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	destination := t.TempDir()
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{Kubernetes: &builderv0.KubernetesDeployment{
			Namespace: "codefly", Destination: destination, Profile: profile,
		}}},
		DependenciesConfigurations: configurations,
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               DeploymentInputs{DependencyConfigurations: true},
		Parameters:           struct{ Name string }{Name: "gitops"},
		Prepare:              prepare,
	})
	return destination, response, err
}

func readDocuments(t *testing.T, path string) []map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	var documents []map[string]any
	for {
		var document map[string]any
		if decoder.Decode(&document) != nil {
			return documents
		}
		documents = append(documents, document)
	}
}

// A workload rendered for a Kubernetes cell with a group whose values are
// larger than the environment can carry: its ConfigMap, which the container
// loads as environment, carries each large value only as a path; the values
// are in a ConfigMap mounted read-only into the container; every small value
// stays inline; no environment value reaches Linux's 128 KiB limit; and the
// render still passes the restricted manifest contract.
func TestARenderedWorkloadReceivesLargeValuesAsMountedFiles(t *testing.T) {
	info := &basev0.ConfigurationInformation{Name: "catalog"}
	for i := 0; i < 4; i++ {
		info.ConfigurationValues = append(info.ConfigurationValues,
			&basev0.ConfigurationValue{Key: fmt.Sprintf("BUNDLE_%02d", i), Value: strings.Repeat(fmt.Sprintf("%d", i), resources.MaxEnvironmentStringBytes+1)},
			&basev0.ConfigurationValue{Key: fmt.Sprintf("MODE_%02d", i), Value: "fast"},
		)
	}
	configuration := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}

	destination, response, err := renderWithConfiguration(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, configuration)
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
	require.True(t, response.GetDeployment().GetKubernetes().GetValidation().GetRestricted(), response.GetDeployment().GetKubernetes().GetValidation().GetViolations())

	environment := readDocuments(t, filepath.Join(destination, "base", "config-map.yaml"))[0]["data"].(map[string]any)
	total := 0
	for key, value := range environment {
		entry := key + "=" + value.(string)
		require.Less(t, len(entry)+1, resources.MaxEnvironmentStringBytes, key)
		total += len(entry) + 1
	}
	require.Less(t, total, resources.MaxCarriedEnvironmentBytes)

	files := readDocuments(t, filepath.Join(destination, "overlays", "test", "configuration-files.yaml"))
	require.Len(t, files, 1, "public values only: no Secret")
	require.Equal(t, "ConfigMap", files[0]["kind"])
	require.Equal(t, "cmf-worker", files[0]["metadata"].(map[string]any)["name"])
	data := files[0]["data"].(map[string]any)
	require.Len(t, data, 4, "one file per value too large to carry inline")
	for i := 0; i < 4; i++ {
		carried := resources.WorkspaceConfigurationPrefix + fmt.Sprintf("__CATALOG__BUNDLE_%02d", i)
		require.NotContains(t, environment, carried)
		require.Equal(t, resources.KubernetesFileCarrierMount+"/"+carried, environment[resources.FileCarrierKey(carried)])
		require.Equal(t, strings.Repeat(fmt.Sprintf("%d", i), resources.MaxEnvironmentStringBytes+1), data[carried])
		require.Equal(t, "fast", environment[resources.WorkspaceConfigurationPrefix+fmt.Sprintf("__CATALOG__MODE_%02d", i)])
	}
	kustomization, err := os.ReadFile(filepath.Join(destination, "overlays", "test", "kustomization.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(kustomization), "configuration-files.yaml")

	deployment := readDocuments(t, filepath.Join(destination, "base", "deployment.yaml"))[0]
	pod := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	require.Contains(t, pod["volumes"], map[string]any{"name": "codefly-configuration-files", "configMap": map[string]any{"name": "cmf-worker", "defaultMode": 0o444}})
	container := pod["containers"].([]any)[0].(map[string]any)
	require.Contains(t, container["volumeMounts"], map[string]any{"name": "codefly-configuration-files", "mountPath": resources.KubernetesFileCarrierMount, "readOnly": true})
	requireReadableOnlyByTheWorkload(t, pod)
}

// A workload with nothing large renders exactly as before: no file manifest,
// no volume.
func TestARenderWithoutLargeValuesMountsNothing(t *testing.T) {
	small := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "settings", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "mode", Value: "fast"}},
	}}}
	destination, _, err := renderWithConfiguration(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, small)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(destination, "overlays", "test", "configuration-files.yaml"))
	require.True(t, os.IsNotExist(err))
	deployment, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(deployment), "codefly-configuration-files")
}

// A local apply carries secret values; a large secret is a Secret volume,
// readable by the pod's group and nobody else, never a ConfigMap entry.
func TestALargeSecretIsDeliveredByASecretVolume(t *testing.T) {
	large := strings.Repeat("s", resources.FileCarrierThreshold+1)
	secret := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "vault", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bundle", Value: large, Secret: true}},
	}}}
	destination, response, err := renderWithConfiguration(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1, secret)
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())

	files := readDocuments(t, filepath.Join(destination, "overlays", "test", "configuration-files.yaml"))
	require.Len(t, files, 1)
	require.Equal(t, "Secret", files[0]["kind"])
	for _, path := range []string{filepath.Join(destination, "base", "config-map.yaml"), filepath.Join(destination, "overlays", "test", "configuration-files.yaml")} {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(content), large, "a secret is never rendered in plaintext")
	}
	deployment := readDocuments(t, filepath.Join(destination, "base", "deployment.yaml"))[0]
	pod := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	require.Contains(t, pod["volumes"], map[string]any{"name": "codefly-secret-configuration-files", "secret": map[string]any{"secretName": "secretf-worker", "defaultMode": 0o440}})
	requireReadableOnlyByTheWorkload(t, pod)
}

// requireReadableOnlyByTheWorkload holds a rendered pod's carrier volumes to
// the user its securityContext says it runs as. Kubernetes makes a volume's
// files owned by root and, when the pod declares an fsGroup, group-owned by
// it, adding that group to every container. So: a public file is readable by
// whoever the container runs as; a secret file is readable by the fsGroup —
// which must be the group the pod runs as — and never by others.
func requireReadableOnlyByTheWorkload(t *testing.T, pod map[string]any) {
	t.Helper()
	security, _ := pod["securityContext"].(map[string]any)
	require.Equal(t, true, security["runAsNonRoot"], "the workload runs as a non-root user")
	runAs := security["runAsGroup"]
	if runAs == nil {
		runAs = security["runAsUser"]
	}
	for _, raw := range pod["volumes"].([]any) {
		volume := raw.(map[string]any)
		if secret, ok := volume["secret"].(map[string]any); ok && strings.HasPrefix(volume["name"].(string), "codefly-") {
			mode := secret["defaultMode"].(int)
			require.Zero(t, mode&0o007, "a secret file is never readable by others")
			require.NotZero(t, mode&0o040, "a secret file is readable by the pod's fsGroup")
			require.NotNil(t, security["fsGroup"], "a secret volume needs an fsGroup, or its root-owned files are unreadable to a non-root user")
			require.Equal(t, runAs, security["fsGroup"], "the fsGroup is the group the workload runs as")
		}
		if configMap, ok := volume["configMap"].(map[string]any); ok && strings.HasPrefix(volume["name"].(string), "codefly-") {
			require.Equal(t, 0o444, configMap["defaultMode"], "a public file is readable whatever user the container runs as")
		}
	}
}

// A pod that says what user it runs as keeps its own fsGroup when it declares
// one, and otherwise gets the group it runs as; one that says nothing cannot
// be given a secret file only its user reads, and the render refuses it.
func TestASecretCarrierFollowsThePodsSecurityContext(t *testing.T) {
	mount := []fileCarrierMount{{name: secretFileCarrierVolume, mountPath: resources.KubernetesSecretFileCarrierMount, secret: "secretf-worker"}}
	render := func(securityContext string) (map[string]any, error) {
		manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: worker\nspec:\n  template:\n    spec:\n" + securityContext +
			"      containers:\n        - name: service\n          image: example/worker\n"
		var root yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte(manifest), &root))
		if _, err := mountIntoWorkload(root.Content[0], mount); err != nil {
			return nil, err
		}
		out, err := yaml.Marshal(&root)
		require.NoError(t, err)
		var deployment map[string]any
		require.NoError(t, yaml.Unmarshal(out, &deployment))
		return deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any), nil
	}
	for name, context := range map[string]string{
		"runAsUser":  "      securityContext:\n        runAsNonRoot: true\n        runAsUser: 10001\n",
		"runAsGroup": "      securityContext:\n        runAsNonRoot: true\n        runAsUser: 10001\n        runAsGroup: 20002\n",
		"fsGroup":    "      securityContext:\n        runAsNonRoot: true\n        runAsGroup: 30003\n        fsGroup: 30003\n",
	} {
		pod, err := render(context)
		require.NoError(t, err, name)
		requireReadableOnlyByTheWorkload(t, pod)
	}
	_, err := render("      securityContext:\n        runAsNonRoot: true\n")
	require.ErrorContains(t, err, "needs the pod to declare the user it runs as")
}

// A file object the API server would refuse fails the render instead.
func TestAFileCarrierObjectAboveTheObjectLimitFailsTheRender(t *testing.T) {
	info := &basev0.ConfigurationInformation{Name: "huge"}
	for i := 0; i < 20; i++ {
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: "part" + strings.Repeat("x", i), Value: strings.Repeat("v", 64<<10)})
	}
	_, response, err := renderWithConfiguration(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
		&basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}})
	require.NoError(t, err)
	require.NotEqual(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "object limit")
}

// A value that cannot be delivered by file — here one a plugin adds to the
// workload's environment itself — and that the kubelet could not start the
// container with fails the render, naming the key, instead of reaching the
// cluster as a crash-looping pod.
func TestARenderWhoseEnvironmentExceedsTheLinuxStringLimitFails(t *testing.T) {
	_, response, err := renderPrepared(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
		func(_ context.Context, deployment *KustomizeDeploymentContext) error {
			deployment.ConfigMap = append(deployment.ConfigMap, resources.Env("CODEFLY__PLUGIN_VALUE", strings.Repeat("v", resources.MaxEnvironmentStringBytes)))
			return nil
		})
	require.NoError(t, err)
	require.NotEqual(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "CODEFLY__PLUGIN_VALUE")
	require.Contains(t, response.GetState().GetMessage(), "MAX_ARG_STRLEN")
}
