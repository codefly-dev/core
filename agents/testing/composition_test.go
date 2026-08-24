package testing_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	agents_services "github.com/codefly-dev/core/agents/services"
	agents_testing "github.com/codefly-dev/core/agents/testing"
	"github.com/codefly-dev/core/resources"
)

// fakeService mimics an agent's Service struct for this helper's own
// unit tests — small enough to be kept here so we don't drag any agent
// dependency into core/agents/testing's test module.
type fakeService struct {
	base *agents_services.Base
}

func (f *fakeService) GetBase() *agents_services.Base { return f.base }

func TestAssertBaseWired_NonNilBasePasses(t *testing.T) {
	base := agents_services.NewServiceBase(context.Background(), &resources.Agent{
		Kind: "codefly:service", Name: "test", Version: "0.0.0",
	})
	agents_testing.AssertBaseWired(t, &fakeService{base: base})
}

// fakeSettings is a tiny reflect target so the YAML helper can be
// exercised without pulling any real agent's Settings into this module.
type fakeSettings struct {
	HotReload bool   `yaml:"hot-reload"`
	Name      string `yaml:"name"`
}

func TestAssertYAMLRoundTrip_PopulatesFields(t *testing.T) {
	agents_testing.AssertYAMLRoundTrip(t,
		`
hot-reload: true
name: widget
`,
		func(t *testing.T, s *fakeSettings) {
			if !s.HotReload {
				t.Error("HotReload not populated")
			}
			if s.Name != "widget" {
				t.Errorf("Name = %q, want widget", s.Name)
			}
		})
}

// sampleDeploymentTemplates models a service agent that has NOT re-implemented
// the pod-overlay binding in its template — the state of service-nextjs,
// service-postgres, service-redis, service-vault. Core must render the
// ServiceAccount identity for it from the overlay parameter alone.
func sampleDeploymentTemplates() fstest.MapFS {
	return fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl": {
			Data: []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n"),
		},
		"templates/deployment/kustomize/base/deployment.yaml.tmpl": {Data: []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  selector:
    matchLabels:
      app: {{ .Name }}
  template:
    metadata:
      labels:
        app: {{ .Name }}
    spec:
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: service
          image: {{ .Image }}
          securityContext:
            allowPrivilegeEscalation: false
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            seccompProfile:
              type: RuntimeDefault
            capabilities:
              drop: [ALL]
          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              cpu: 100m
              memory: 64Mi
          startupProbe:
            exec:
              command: ["true"]
          readinessProbe:
            exec:
              command: ["true"]
          livenessProbe:
            exec:
              command: ["true"]
`)},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {
			Data: []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../../base\n"),
		},
	}
}

func TestAssertKustomizeTemplates_RendersAndValidates(t *testing.T) {
	dir := agents_testing.AssertKustomizeTemplates(t, sampleDeploymentTemplates(), nil)
	content, err := os.ReadFile(filepath.Join(dir, "base", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "name: example-service") {
		t.Fatalf("unexpected rendered deployment:\n%s", content)
	}
}

func TestAssertKustomizeTemplatesWithOverlay_RendersServiceAccount(t *testing.T) {
	dir := agents_testing.AssertKustomizeTemplatesWithOverlay(t, sampleDeploymentTemplates(), nil,
		&agents_services.PodTemplateOverlay{
			ServiceAccount: &agents_services.WorkloadServiceAccount{
				Annotations: map[string]string{"azure.workload.identity/client-id": "00000000-0000-0000-0000-000000000000"},
			},
			PodLabels: map[string]string{"azure.workload.identity/use": "true"},
		})

	deployment, err := os.ReadFile(filepath.Join(dir, "base", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deployment), "serviceAccountName: example-service") {
		t.Fatalf("overlay did not render serviceAccountName:\n%s", deployment)
	}
	if !strings.Contains(string(deployment), `azure.workload.identity/use: "true"`) {
		t.Fatalf("overlay did not render pod label:\n%s", deployment)
	}

	sa, err := os.ReadFile(filepath.Join(dir, "base", "serviceaccount.yaml"))
	if err != nil {
		t.Fatalf("overlay did not emit a ServiceAccount object: %v", err)
	}
	for _, want := range []string{"kind: ServiceAccount", "name: example-service", "azure.workload.identity/client-id: 00000000-0000-0000-0000-000000000000"} {
		if !strings.Contains(string(sa), want) {
			t.Fatalf("ServiceAccount missing %q:\n%s", want, sa)
		}
	}
}
