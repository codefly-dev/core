package sdk

import (
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

func secretsDep(endpoints ...string) []*resources.ServiceDependency {
	dep := &resources.ServiceDependency{Module: "vault", Name: "secrets"}
	for _, name := range endpoints {
		dep.Endpoints = append(dep.Endpoints, &resources.EndpointReference{Name: name})
	}
	return []*resources.ServiceDependency{dep}
}

func mappingOf(name, visibility string, allow ...string) *basev0.NetworkMapping {
	return &basev0.NetworkMapping{Endpoint: &basev0.Endpoint{
		Module:       "vault",
		Service:      "secrets",
		Name:         name,
		Visibility:   visibility,
		AllowModules: allow,
	}}
}

// A consumed endpoint whose visibility forbids the consuming module is rejected;
// the same endpoint that names the module in its allow-list is accepted.
func TestValidateConsumedMappingVisibilityEnforcesConsumedEndpoint(t *testing.T) {
	deps := secretsDep("http")
	mappings := []*basev0.NetworkMapping{mappingOf("http", resources.VisibilityInternal, "platform")}

	if err := validateConsumedMappingVisibility("web", deps, mappings); err == nil {
		t.Fatal("consuming an internal endpoint that does not permit the module must fail")
	}
	if err := validateConsumedMappingVisibility("platform", deps, mappings); err != nil {
		t.Fatalf("allow-listed module must be permitted: %v", err)
	}
}

// The dependency graph may surface a producer's sibling endpoint that this
// service never named. Its visibility must NOT fail the run — only consumed
// endpoints are enforced, matching the static workspace pass.
func TestValidateConsumedMappingVisibilityIgnoresUnconsumedSiblingEndpoint(t *testing.T) {
	deps := secretsDep("http") // consumes only "http"
	mappings := []*basev0.NetworkMapping{
		mappingOf("http", resources.VisibilityPublic),
		mappingOf("admin", resources.VisibilityPrivate), // sibling the consumer never asked for
	}

	if err := validateConsumedMappingVisibility("web", deps, mappings); err != nil {
		t.Fatalf("an unconsumed sibling endpoint must not fail the run: %v", err)
	}
}

// An unnamed dependency consumes every endpoint, so a private sibling IS
// enforced in that case — the scoping must key off actual consumption.
func TestValidateConsumedMappingVisibilityUnnamedDependencyConsumesAll(t *testing.T) {
	deps := secretsDep() // no endpoint names -> consumes them all
	mappings := []*basev0.NetworkMapping{mappingOf("admin", resources.VisibilityPrivate)}

	if err := validateConsumedMappingVisibility("web", deps, mappings); err == nil {
		t.Fatal("an unnamed dependency consumes every endpoint; a private cross-module one must fail")
	}
}

// A mapping without an endpoint must return an error rather than dereferencing
// a nil pointer and panicking the runtime.
func TestValidateConsumedMappingVisibilityRejectsNilEndpoint(t *testing.T) {
	deps := secretsDep("http")
	err := validateConsumedMappingVisibility("platform", deps, []*basev0.NetworkMapping{{Endpoint: nil}})
	if err == nil || !strings.Contains(err.Error(), "missing its endpoint") {
		t.Fatalf("nil endpoint must be rejected, got %v", err)
	}
}
