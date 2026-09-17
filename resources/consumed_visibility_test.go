package resources_test

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

func resolvedNames(mappings []*basev0.NetworkMapping) []string {
	names := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		names = append(names, mapping.GetEndpoint().GetName())
	}
	return names
}

// A consumed endpoint whose visibility forbids the consuming module is rejected;
// the same endpoint that names the module in its allow-list is accepted.
func TestResolveDependencyNetworkMappingsEnforcesConsumedEndpoint(t *testing.T) {
	deps := secretsDep("http")
	mappings := []*basev0.NetworkMapping{mappingOf("http", resources.VisibilityInternal, "platform")}

	if _, err := resources.ResolveDependencyNetworkMappings("web", deps, mappings); err == nil {
		t.Fatal("consuming an internal endpoint that does not permit the module must fail")
	}
	resolved, err := resources.ResolveDependencyNetworkMappings("platform", deps, mappings)
	if err != nil {
		t.Fatalf("allow-listed module must be permitted: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("the permitted endpoint must be resolved, got %v", resolvedNames(resolved))
	}
}

// The dependency graph may surface a producer's sibling endpoint that this
// service never named. Its visibility must NOT fail the run — only consumed
// endpoints are enforced, matching the static workspace pass.
func TestResolveDependencyNetworkMappingsIgnoresUnconsumedSiblingEndpoint(t *testing.T) {
	deps := secretsDep("http") // consumes only "http"
	mappings := []*basev0.NetworkMapping{
		mappingOf("http", resources.VisibilityPublic),
		mappingOf("admin", resources.VisibilityPrivate), // sibling the consumer never asked for
	}

	resolved, err := resources.ResolveDependencyNetworkMappings("web", deps, mappings)
	if err != nil {
		t.Fatalf("an unconsumed sibling endpoint must not fail the run: %v", err)
	}
	if got := resolvedNames(resolved); len(got) != 1 || got[0] != "http" {
		t.Fatalf("only the named endpoint may be exposed, got %v", got)
	}
}

// Naming an endpoint the producer keeps private is an error that names the
// endpoint. Filtering it away instead would leave the consumer's SDK missing an
// accessor its author explicitly declared, with nothing to explain why.
func TestResolveDependencyNetworkMappingsRejectsNamedForbiddenEndpoint(t *testing.T) {
	deps := secretsDep("http", "admin")
	mappings := []*basev0.NetworkMapping{
		mappingOf("http", resources.VisibilityPublic),
		mappingOf("admin", resources.VisibilityPrivate),
	}

	_, err := resources.ResolveDependencyNetworkMappings("web", deps, mappings)
	if err == nil {
		t.Fatal("naming a private endpoint must fail rather than silently drop it")
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Fatalf("the error must name the offending endpoint, got %v", err)
	}
}

// A dependency that names no endpoint consumes "all", which can only mean all
// it is permitted: the forbidden ones are filtered out and the rest still
// resolve. Failing the whole edge instead would let a producer break every
// consumer that did not enumerate simply by adding one private endpoint.
func TestResolveDependencyNetworkMappingsFiltersForbiddenWhenConsumingAll(t *testing.T) {
	deps := secretsDep() // no endpoint names -> consumes them all
	mappings := []*basev0.NetworkMapping{
		mappingOf("http", resources.VisibilityPublic),
		mappingOf("admin", resources.VisibilityPrivate),
		mappingOf("ops", resources.VisibilityInternal, "platform"),
	}

	resolved, err := resources.ResolveDependencyNetworkMappings("web", deps, mappings)
	if err != nil {
		t.Fatalf("a private endpoint the consumer never named must not fail the run: %v", err)
	}
	if got := resolvedNames(resolved); len(got) != 1 || got[0] != "http" {
		t.Fatalf("the exposed set must be exactly the permitted set, got %v", got)
	}

	// The same producer, seen by a module its internal endpoint allows.
	resolved, err = resources.ResolveDependencyNetworkMappings("platform", deps, mappings)
	if err != nil {
		t.Fatalf("allow-listed module must resolve its permitted endpoints: %v", err)
	}
	got := resolvedNames(resolved)
	if len(got) != 2 || got[0] != "http" || got[1] != "ops" {
		t.Fatalf("both permitted endpoints must be exposed, got %v", got)
	}
}

// An intra-module edge is always permitted, whatever the endpoint's visibility.
func TestResolveDependencyNetworkMappingsAllowsSameModulePrivateEndpoint(t *testing.T) {
	deps := secretsDep("admin")
	mappings := []*basev0.NetworkMapping{mappingOf("admin", resources.VisibilityPrivate)}

	resolved, err := resources.ResolveDependencyNetworkMappings("vault", deps, mappings)
	if err != nil {
		t.Fatalf("a module may consume its own private endpoint: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("the endpoint must resolve, got %v", resolvedNames(resolved))
	}
}

// A mapping without an endpoint must return an error rather than dereferencing
// a nil pointer and panicking the runtime.
func TestResolveDependencyNetworkMappingsRejectsNilEndpoint(t *testing.T) {
	deps := secretsDep("http")
	_, err := resources.ResolveDependencyNetworkMappings("platform", deps, []*basev0.NetworkMapping{{Endpoint: nil}})
	if err == nil || !strings.Contains(err.Error(), "missing its endpoint") {
		t.Fatalf("nil endpoint must be rejected, got %v", err)
	}
}
