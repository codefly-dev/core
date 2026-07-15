package testing_test

import (
	"context"
	"os"
	"path/filepath"
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

func TestAssertKustomizeTemplates_RendersAndValidates(t *testing.T) {
	templates := fstest.MapFS{
		"templates/deployment/kustomize/base/kustomization.yaml.tmpl":                 {Data: []byte("resources:\n  - namespace.yaml\n")},
		"templates/deployment/kustomize/base/namespace.yaml.tmpl":                     {Data: []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: {{ .Namespace }}\n")},
		"templates/deployment/kustomize/overlays/environment/kustomization.yaml.tmpl": {Data: []byte("resources:\n  - ../../base\n")},
	}
	dir := agents_testing.AssertKustomizeTemplates(t, templates, nil)
	content, err := os.ReadFile(filepath.Join(dir, "base", "namespace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: codefly-test\n" {
		t.Fatalf("unexpected rendered namespace:\n%s", content)
	}
}
