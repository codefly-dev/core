// Package testing provides helpers for agent composition + lifecycle
// tests. The goal is to make adding tests to a new agent cheap — one
// import and a couple of one-line calls, matching the pattern the 13
// service agents use (Service with services.Base, Settings with YAML,
// PluginRegistration shape).
//
// This package is test-only in practice: callers import it from their
// agent's `composition_test.go`. It deliberately avoids spinning up
// actual subprocesses, network, or Docker — those belong in each
// agent's own integration tests.
package testing

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// BaseHolder is the minimal shape every agent's Service satisfies: it
// embeds or holds a pointer to a services.Base. Composition tests only
// need to reach into Base to verify wiring.
//
// Agents can satisfy this trivially — their Service already embeds
// *services.Base, so the interface is auto-implemented by promotion.
type BaseHolder interface {
	// GetBase returns the underlying services.Base. Most agents embed
	// *Base anonymously, in which case they need a one-line shim:
	//
	//	func (s *Service) GetBase() *services.Base { return s.Base }
	//
	// Zero cost at runtime; provides a stable test-only access point.
	GetBase() *services.Base
}

// AssertBaseWired is the canonical composition sanity check. Every
// service agent should call this in its composition_test.go.
//
//	func TestNewService_EmbedsBase(t *testing.T) {
//	    testing.AssertBaseWired(t, NewService())
//	}
//
// On failure the test exits immediately with a diagnostic pointing at
// the field that's nil — common causes are (a) someone forgot to call
// services.NewServiceBase, (b) the agent type stopped embedding *Base.
func AssertBaseWired(t *testing.T, holder BaseHolder) {
	t.Helper()
	if holder == nil {
		t.Fatal("service holder is nil")
	}
	base := holder.GetBase()
	if base == nil {
		t.Fatal("services.Base is nil — did NewServiceBase get called?")
	}
}

// AssertYAMLRoundTrip unmarshals a YAML string into a fresh zero-valued
// settings struct and calls check(settings) for field-level assertions.
// The check closure gets to run arbitrary expectations without the
// caller needing to redeclare the Settings type by hand.
//
//	testing.AssertYAMLRoundTrip(t, `hot-reload: true`, func(t *testing.T, s *Settings) {
//	    if !s.HotReload { t.Error("HotReload not populated") }
//	})
//
// The function is generic so each caller types its own Settings — this
// avoids leaking agent-specific symbols into this shared package.
func AssertYAMLRoundTrip[S any](t *testing.T, yamlDoc string, check func(t *testing.T, s *S)) {
	t.Helper()
	var out S
	if err := yaml.Unmarshal([]byte(yamlDoc), &out); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	check(t, &out)
}

// MissingField builds a Skip message for cases where an agent has
// intentionally no Settings (e.g. the generic agent). Prefer explicit
// nil-tolerance over skipping; this is provided only for corner cases
// where a shared helper doesn't apply.
func MissingField(field string) string {
	return fmt.Sprintf("%s not populated (intentional for agents without this setting)", field)
}

// AssertKustomizeTemplates runs both v1 output profiles and the shared hostile
// contract suite against a plugin's embedded deployment templates.
//
// The returned directory can be used for plugin-specific assertions:
//
//	dir := agenttesting.AssertKustomizeTemplates(t, deploymentFS, Parameters{})
//	manifest, _ := os.ReadFile(filepath.Join(dir, "base", "deployment.yaml"))
func AssertKustomizeTemplates(t *testing.T, templates fs.FS, parameters any) string {
	t.Helper()
	AssertKubernetesManifestContract(t)
	ephemeral := assertKustomizeProfile(
		t,
		templates,
		parameters,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
	)
	assertKustomizeProfile(
		t,
		templates,
		parameters,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
	)
	return ephemeral
}

func assertKustomizeProfile(
	t *testing.T,
	templates fs.FS,
	parameters any,
	profile builderv0.KubernetesOutputProfile,
) string {
	t.Helper()
	ctx := context.Background()
	identity := &resources.ServiceIdentity{
		Workspace: "workspace",
		Module:    "module",
		Name:      "example-service",
		Version:   "1.2.3",
	}
	base := &services.Base{
		Wool:        wool.Get(ctx),
		Identity:    identity,
		Information: &services.Information{Service: resources.ToServiceWithCase(identity), Module: resources.ToModuleWithCase(identity)},
	}
	if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 {
		base.SetDockerImage(&resources.DockerImage{
			Name:   "example/service",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
	} else {
		base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	}
	builder := &services.BuilderWrapper{Base: base}
	base.Builder = builder

	destination := t.TempDir()
	deployment := &builderv0.KubernetesDeployment{
		Namespace:   "codefly-test",
		Destination: destination,
		Profile:     profile,
		SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{
			"CODEFLY_TEST_SECRET": {Name: "example-service-secrets", Key: "test-secret"},
		},
	}
	params := services.DeploymentParameters{
		ConfigMap:        services.EnvironmentMap{"CODEFLY_TEST_VALUE": "value"},
		SecretReferences: deployment.GetSecretReferences(),
		Parameters:       parameters,
	}
	if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1 {
		params.SecretMap = services.EnvironmentMap{"CODEFLY_TEST_SECRET": "c2VjcmV0"}
	}
	if err := builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, deployment, templates, params); err != nil {
		t.Fatalf("render %s kustomize templates: %v", profile, err)
	}

	validation := services.ValidateKubernetesManifestTree(ctx, destination, "test", "codefly-test", profile, false)
	if validation.GetStaticValidation() != builderv0.KubernetesManifestValidation_STATUS_PASSED {
		t.Fatalf("%s static conformance failed:\n%s", profile, strings.Join(validation.GetViolations(), "\n"))
	}
	if profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 {
		err := filepath.WalkDir(destination, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(content), "c2VjcmV0") {
				return fmt.Errorf("%s contains the hostile secret sentinel", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

// AssertKubernetesManifestContract exercises the common rejection cases every
// official plugin must retain when it adopts AssertKustomizeTemplates.
func AssertKubernetesManifestContract(t *testing.T) {
	t.Helper()
	positive := restrictedDeploymentManifest
	positive += `
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: example-service-secrets
  namespace: codefly-test
spec:
  secretStoreRef:
    name: application-secrets
    kind: SecretStore
  data:
    - secretKey: password
      remoteRef:
        key: applications/example-service
        property: password
`
	assertContractResult(t, "positive", positive, builderv0.KubernetesManifestValidation_STATUS_PASSED, "")

	tests := []struct {
		name     string
		manifest string
		contains string
	}{
		{
			name:     "floating image",
			manifest: strings.Replace(restrictedDeploymentManifest, restrictedImage, "example/service:latest", 1),
			contains: "not pinned by sha256 digest",
		},
		{
			name: "inline Secret data",
			manifest: restrictedDeploymentManifest + `
---
apiVersion: v1
kind: Secret
metadata:
  name: inline-secret
  namespace: codefly-test
stringData:
  password: hostile
`,
			contains: "emits a Secret resource",
		},
		{
			name:     "service account token",
			manifest: strings.Replace(restrictedDeploymentManifest, "automountServiceAccountToken: false", "automountServiceAccountToken: true", 1),
			contains: "automountServiceAccountToken: false",
		},
		{
			name:     "privilege escalation",
			manifest: strings.Replace(restrictedDeploymentManifest, "allowPrivilegeEscalation: false", "allowPrivilegeEscalation: true", 1),
			contains: "allowPrivilegeEscalation: false",
		},
		{
			name:     "capabilities",
			manifest: strings.Replace(restrictedDeploymentManifest, "drop:\n                - ALL", "drop: []", 1),
			contains: "drop ALL capabilities",
		},
		{
			name:     "host access",
			manifest: strings.Replace(restrictedDeploymentManifest, "automountServiceAccountToken: false", "automountServiceAccountToken: false\n      hostNetwork: true", 1),
			contains: "must not enable hostNetwork",
		},
		{
			name: "unsafe service",
			manifest: restrictedDeploymentManifest + `
---
apiVersion: v1
kind: Service
metadata:
  name: example-service
  namespace: codefly-test
spec:
  type: NodePort
  selector:
    app: example-service
  ports:
    - port: 8080
      nodePort: 30080
`,
			contains: "unsafe service type",
		},
		{
			name: "unowned namespace",
			manifest: restrictedDeploymentManifest + `
---
apiVersion: v1
kind: Namespace
metadata:
  name: codefly-test
`,
			contains: "app.kubernetes.io/managed-by",
		},
		{
			name: "cluster RBAC",
			manifest: restrictedDeploymentManifest + `
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hostile
rules: []
`,
			contains: "prohibited cluster-scoped resource",
		},
		{
			name:     "unresolved placeholder",
			manifest: strings.Replace(restrictedDeploymentManifest, "name: example-service", "name: ${SERVICE_NAME}", 1),
			contains: "unresolved placeholder",
		},
		{
			name:     "missing resource bounds",
			manifest: strings.Replace(restrictedDeploymentManifest, restrictedResources, "", 1),
			contains: "has no resource requests or limits",
		},
		{
			name:     "missing liveness",
			manifest: strings.Replace(restrictedDeploymentManifest, restrictedLivenessProbe, "", 1),
			contains: "valid livenessProbe",
		},
	}
	for _, test := range tests {
		assertContractResult(t, test.name, test.manifest, builderv0.KubernetesManifestValidation_STATUS_FAILED, test.contains)
	}
	assertInvalidKustomization(t)
}

func assertInvalidKustomization(t *testing.T) {
	t.Helper()
	t.Run("contract/invalid kustomization", func(t *testing.T) {
		destination := t.TempDir()
		base := filepath.Join(destination, "base")
		overlay := filepath.Join(destination, "overlays", "test")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(overlay, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "kustomization.yaml"), []byte(
			"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - missing.yaml\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(overlay, "kustomization.yaml"), []byte(
			"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../../base\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}
		validation := services.ValidateKubernetesManifestTree(
			context.Background(),
			destination,
			"test",
			"codefly-test",
			builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			false,
		)
		if validation.GetStaticValidation() != builderv0.KubernetesManifestValidation_STATUS_FAILED {
			t.Fatalf("status = %s, want failed", validation.GetStaticValidation())
		}
		if !strings.Contains(strings.Join(validation.GetViolations(), "\n"), "kustomize build") {
			t.Fatalf("violations do not report Kustomize failure: %v", validation.GetViolations())
		}
	})
}

func assertContractResult(
	t *testing.T,
	name string,
	manifest string,
	expected builderv0.KubernetesManifestValidation_Status,
	contains string,
) {
	t.Helper()
	t.Run("contract/"+name, func(t *testing.T) {
		destination := t.TempDir()
		base := filepath.Join(destination, "base")
		overlay := filepath.Join(destination, "overlays", "test")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(overlay, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			filepath.Join(base, "kustomization.yaml"): `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - resources.yaml
`,
			filepath.Join(base, "resources.yaml"): manifest,
			filepath.Join(overlay, "kustomization.yaml"): `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`,
		}
		for path, content := range files {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		validation := services.ValidateKubernetesManifestTree(
			context.Background(),
			destination,
			"test",
			"codefly-test",
			builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
			false,
		)
		if validation.GetStaticValidation() != expected {
			t.Fatalf("status = %s, want %s; violations: %v", validation.GetStaticValidation(), expected, validation.GetViolations())
		}
		if contains != "" && !strings.Contains(strings.Join(validation.GetViolations(), "\n"), contains) {
			t.Fatalf("violations do not contain %q: %v", contains, validation.GetViolations())
		}
	})
}

const restrictedImage = "example/service@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const restrictedResources = `          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              cpu: 100m
              memory: 64Mi
`

const restrictedLivenessProbe = `          livenessProbe:
            exec:
              command: ["true"]
`

const restrictedDeploymentManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-service
  namespace: codefly-test
spec:
  replicas: 1
  selector:
    matchLabels:
      app: example-service
  template:
    metadata:
      labels:
        app: example-service
    spec:
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: service
          image: ` + restrictedImage + `
          securityContext:
            allowPrivilegeEscalation: false
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            seccompProfile:
              type: RuntimeDefault
            capabilities:
              drop:
                - ALL
` + restrictedResources + `          startupProbe:
            exec:
              command: ["true"]
          readinessProbe:
            exec:
              command: ["true"]
` + restrictedLivenessProbe + `
`
