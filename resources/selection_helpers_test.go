package resources_test

import (
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

// declaring answers a DeclaredEndpoints lookup over the endpoints given, keyed
// by the <module>/<service> each one carries. A producer none of them name is
// unknown to the lookup, which is the fact the resolution refuses on.
func declaring(endpoints ...*resources.Endpoint) resources.DeclaredEndpoints {
	byUnique := map[string][]*resources.Endpoint{}
	for _, endpoint := range endpoints {
		unique := endpoint.Module + "/" + endpoint.Service
		byUnique[unique] = append(byUnique[unique], endpoint)
	}
	return func(unique string) ([]*resources.Endpoint, bool) {
		declared, ok := byUnique[unique]
		return declared, ok
	}
}

// gatewayDeclared is the manifest the interpolation tests resolve against: every
// producer a test references is DECLARED here, so that an endpoint absent from a
// test's mappings is "declared but not published for this consumer" (an
// availability fact the run-wide path may drop on) and never "a producer this
// workspace does not declare" (a composition fault it must refuse on). The
// gateway producer declares both an http and a grpc endpoint for the same
// reason.
func gatewayDeclared() resources.DeclaredEndpoints {
	public := func(module, service, name, api string) *resources.Endpoint {
		return &resources.Endpoint{Module: module, Service: service, Name: name, API: api, Visibility: "public", Exposure: "none"}
	}
	return declaring(
		public("edge", "sidecar", "http", standards.HTTP),
		public("edge", "sidecar", "grpc", standards.GRPC),
		public("infra", "temporal", "grpc", standards.GRPC),
		public("mod", "store", "postgres", standards.TCP),
		public("mod", "absent", "postgres", standards.TCP),
		public("mod", "other", "http", standards.HTTP),
		public("model", "model", "http", standards.HTTP),
		public("saas", "frontend", "http", standards.HTTP),
		public("saas", "auth-gateway", "rest", standards.REST),
		public("saas", "auth", "http", standards.HTTP),
	)
}

// asGatewayConsumer is the selection every gateway test resolves under: a
// consumer in another module, over the producer's manifest.
func asGatewayConsumer() resources.EndpointSelectionContext {
	return resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: gatewayDeclared()}
}
