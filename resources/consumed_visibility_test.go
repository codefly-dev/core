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

func mappingOf(name, visibility string) *basev0.NetworkMapping {
	endpoint := &basev0.Endpoint{
		Module:     "vault",
		Service:    "secrets",
		Name:       name,
		Visibility: visibility,
	}
	if visibility == resources.VisibilityPublic {
		endpoint.Exposure = resources.ExposureNone
	}
	return &basev0.NetworkMapping{Endpoint: endpoint}
}

func resolvedNames(mappings []*basev0.NetworkMapping) []string {
	names := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		names = append(names, mapping.GetEndpoint().GetName())
	}
	return names
}

// A consumed endpoint whose visibility forbids the consuming module is rejected
// — private stops at the owning module — and an internal one, which names
// nobody, is accepted for whatever composes the workspace.
func TestResolveDependencyNetworkMappingsEnforcesConsumedEndpoint(t *testing.T) {
	deps := secretsDep("http")

	if _, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "web", deps, []*basev0.NetworkMapping{mappingOf("http", resources.VisibilityPrivate)}); err == nil {
		t.Fatal("consuming a private endpoint from another module must fail")
	}
	mappings := []*basev0.NetworkMapping{mappingOf("http", resources.VisibilityInternal)}
	resolved, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "platform", deps, mappings)
	if err != nil {
		t.Fatalf("an internal endpoint is reachable by whatever composes the workspace: %v", err)
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

	resolved, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "web", deps, mappings)
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

	_, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "web", deps, mappings)
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
		mappingOf("ops", resources.VisibilityInternal),
	}

	// The public and the internal endpoints are permitted to any module of the
	// composition; only the private one is filtered.
	for _, consumer := range []string{"web", "platform"} {
		resolved, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), consumer, deps, mappings)
		if err != nil {
			t.Fatalf("a private endpoint the consumer never named must not fail the run for %s: %v", consumer, err)
		}
		if got := resolvedNames(resolved); len(got) != 2 || got[0] != "http" || got[1] != "ops" {
			t.Fatalf("the exposed set must be exactly the permitted set for %s, got %v", consumer, got)
		}
	}
}

// An intra-module edge is always permitted, whatever the endpoint's visibility.
func TestResolveDependencyNetworkMappingsAllowsSameModulePrivateEndpoint(t *testing.T) {
	deps := secretsDep("admin")
	mappings := []*basev0.NetworkMapping{mappingOf("admin", resources.VisibilityPrivate)}

	resolved, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "vault", deps, mappings)
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
	_, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "platform", deps, []*basev0.NetworkMapping{{Endpoint: nil}})
	if err == nil || !strings.Contains(err.Error(), "missing its endpoint") {
		t.Fatalf("nil endpoint must be rejected, got %v", err)
	}
}

// Consuming "all" of a producer that permits none of it is not an empty
// consumption, it is a declared edge the export boundary grants nothing for.
// Resolving it to nothing would leave the consumer wired to a producer it can
// never reach, with no address and no error to say why.
func TestResolveDependencyNetworkMappingsRejectsConsumingAllWhenNothingIsPermitted(t *testing.T) {
	deps := secretsDep() // no endpoint names -> consumes them all
	mappings := []*basev0.NetworkMapping{
		mappingOf("admin", resources.VisibilityPrivate),
		mappingOf("ops", resources.VisibilityPrivate),
	}

	_, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "web", deps, mappings)
	if err == nil {
		t.Fatal("a dependency permitted none of the producer's endpoints must fail")
	}
	for _, want := range []string{"names no endpoint", "admin", "ops", `module "web"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error must carry %q, got %v", want, err)
		}
	}
}

// A producer that exports no endpoint at all is a different case: there is
// nothing to permit, and whether the edge makes sense is the prerequisite
// check's question, not visibility's.
func TestResolveDependencyNetworkMappingsAllowsAnEndpointlessProducer(t *testing.T) {
	deps := secretsDep()

	resolved, err := resources.ResolveDependencyNetworkMappings(composition("web", "vault", "platform"), "web", deps, nil)
	if err != nil {
		t.Fatalf("a producer with no endpoint must not fail visibility: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("nothing can resolve, got %v", resolvedNames(resolved))
	}
}

// composition is the provenance a test states for the modules it uses: a
// workspace carrying them all as modules of one owner, which is what every
// verdict on an edge takes.
func composition(modules ...string) *resources.Workspace {
	workspace := &resources.Workspace{Name: "test"}
	for _, module := range modules {
		workspace.Modules = append(workspace.Modules, &resources.ModuleReference{Name: module})
	}
	return workspace
}
