package configurations_test

import "github.com/codefly-dev/core/resources"

// managerDeclared is the manifest the manager tests resolve against: every
// producer a test references is declared and public, so an endpoint absent from
// a test's mappings is "declared but not published for this consumer" — the
// availability fact the run-wide path drops on — and never a composition fault.
func managerDeclared() resources.DeclaredEndpoints {
	public := func(module, service, name, api string) *resources.Endpoint {
		return &resources.Endpoint{Module: module, Service: service, Name: name, API: api, Visibility: "public"}
	}
	endpoints := []*resources.Endpoint{
		public("absent", "service", "http", "http"),
		public("absent", "store", "postgres", "tcp"),
		public("documents", "store", "grpc", "grpc"),
		public("host", "api", "admin", "grpc"),
		public("host", "api", "grpc", "grpc"),
		public("host", "api", "http", "http"),
		public("host", "gate", "grpc", "grpc"),
		public("infra", "temporal", "grpc", "grpc"),
		public("edge", "sidecar", "http", "http"),
		public("edge", "sidecar", "grpc", "grpc"),
		public("saas", "accounts", "grpc", "grpc"),
		public("saas", "accounts", "rest", "rest"),
		public("saas", "auth-gateway", "rest", "rest"),
		public("saas", "auth", "http", "http"),
		public("saas", "frontend", "http", "http"),
	}
	byUnique := map[string][]*resources.Endpoint{}
	for _, endpoint := range endpoints {
		byUnique[endpoint.Module+"/"+endpoint.Service] = append(byUnique[endpoint.Module+"/"+endpoint.Service], endpoint)
	}
	return func(unique string) ([]*resources.Endpoint, bool) {
		declared, ok := byUnique[unique]
		return declared, ok
	}
}
