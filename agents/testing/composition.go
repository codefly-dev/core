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
	"io"
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

// AssertKustomizeTemplates renders a plugin's embedded deployment templates
// with representative config, secret, image, and plugin parameters. It then
// validates every emitted YAML document and every local resource referenced by
// a kustomization. This catches stale template-context fields, unexpanded Go
// template expressions, malformed YAML, and dangling resource paths without a
// Kubernetes cluster or external binary.
//
// The returned directory can be used for plugin-specific assertions:
//
//	dir := agenttesting.AssertKustomizeTemplates(t, deploymentFS, Parameters{})
//	manifest, _ := os.ReadFile(filepath.Join(dir, "base", "deployment.yaml"))
func AssertKustomizeTemplates(t *testing.T, templates fs.FS, parameters any) string {
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
	base.SetDockerImage(resources.NewDockerImage("example/service:1.2.3"))
	builder := &services.BuilderWrapper{Base: base}
	base.Builder = builder

	destination := t.TempDir()
	deployment := &builderv0.KubernetesDeployment{Namespace: "codefly-test", Destination: destination}
	params := services.DeploymentParameters{
		ConfigMap:  services.EnvironmentMap{"CODEFLY_TEST_VALUE": "value"},
		SecretMap:  services.EnvironmentMap{"CODEFLY_TEST_SECRET": "c2VjcmV0"},
		Parameters: parameters,
	}
	if err := builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, deployment, templates, params); err != nil {
		t.Fatalf("render kustomize templates: %v", err)
	}

	err := filepath.WalkDir(destination, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "{{") || strings.Contains(string(content), "}}") {
			return fmt.Errorf("%s contains an unexpanded template expression", path)
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(content)))
		for {
			var document map[string]any
			if err = decoder.Decode(&document); err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("decode %s: %w", path, err)
			}
			kind, _ := document["kind"].(string)
			if kind == "Secret" || kind == "ConfigMap" {
				if data, exists := document["data"]; exists {
					if _, ok := data.(map[string]any); !ok {
						return fmt.Errorf("%s %s.data must be a mapping, got %T", path, kind, data)
					}
				}
			}
		}
		if filepath.Base(path) == "kustomization.yaml" {
			var kustomization struct {
				Resources []string `yaml:"resources"`
			}
			if err = yaml.Unmarshal(content, &kustomization); err != nil {
				return fmt.Errorf("decode resources in %s: %w", path, err)
			}
			for _, resource := range kustomization.Resources {
				if strings.Contains(resource, "://") {
					continue
				}
				if _, err = os.Stat(filepath.Clean(filepath.Join(filepath.Dir(path), resource))); err != nil {
					return fmt.Errorf("resource %q from %s: %w", resource, path, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return destination
}
