package composition

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

// ProtobufProcedures validates one listener's explicit inventory against its
// descriptor and returns the procedures it serves, sorted. Every listed
// service and procedure must exist in the descriptor, each once; with
// endpoint.Package set, every listed service lies in that package. Imported
// schemas alone never establish serving. That a listener serves a subset is
// legitimate only when its siblings serve the rest: ProtobufCoverage holds a
// descriptor's listeners to that. A service listed with an Interface is the
// protocol of an interface other modules implement: it exists in the
// descriptor and contributes no procedure.
func ProtobufProcedures(set *descriptorpb.FileDescriptorSet, endpoint APIContractEndpoint) ([]string, error) {
	available := map[string]APIContractService{}
	for _, service := range ProtobufServices(set, "") {
		available[service.FullName] = service
	}
	seenServices, seen := map[string]bool{}, map[string]bool{}
	var methods []string
	for _, service := range endpoint.Services {
		declared, exists := available[service.FullName]
		if !exists {
			return nil, fmt.Errorf("service %s is absent from its descriptor", service.FullName)
		}
		if endpoint.Package != "" && servicePackage(declared) != endpoint.Package {
			return nil, fmt.Errorf("service %s is outside the endpoint's package %s", service.FullName, endpoint.Package)
		}
		if service.Name != declared.Name || seenServices[service.FullName] {
			return nil, fmt.Errorf("service %s must use descriptor name %s and appear exactly once", service.FullName, declared.Name)
		}
		seenServices[service.FullName] = true
		if err := service.validateInterface(); err != nil {
			return nil, err
		}
		allowed := map[string]bool{}
		for _, method := range declared.Procedures {
			allowed[method] = true
		}
		for _, method := range service.Procedures {
			if !allowed[method] {
				return nil, fmt.Errorf("procedure %s is absent from its descriptor service %s", method, service.FullName)
			}
			if seen[method] {
				return nil, fmt.Errorf("declares procedure twice: %s", method)
			}
			seen[method] = true
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)
	return methods, nil
}

// ProtobufCoverage holds the listeners that share one descriptor to
// exactness. A restricted listener may serve part of a service — the
// procedures its owner's routing tables give it — but together the listeners
// serve every method of every service any of them lists, and, for each
// package one of them names as its Package, every service of that package. A
// descriptor method no listener serves is a catalog that was not regenerated
// with its descriptor, refused naming the method, because update
// compatibility derives evidence only from served methods and would never see
// a breaking change to it. A service of another package that no listener
// lists — an imported schema, a foreign API the module only calls — is not
// served, and needs no row.
//
// A module may also define, in a package it serves, the protocol of an
// interface it publishes for other modules to implement: its descriptor
// declares the service and none of its listeners serves it. That is exempt
// only when a row says so, naming the interface (APIContractService.Interface)
// — never because the service is merely unlisted, which is the stale catalog
// this guards against — and only when no listener serves any of it. Its
// compatibility is then the interface's own versioning, not this module's
// served evidence.
func ProtobufCoverage(set *descriptorpb.FileDescriptorSet, listeners []APIContractEndpoint) error {
	served, listed, packages := map[string]bool{}, map[string]bool{}, map[string]bool{}
	published, servers := map[string]string{}, map[string]string{}
	for _, listener := range listeners {
		procedures, err := ProtobufProcedures(set, listener)
		if err != nil {
			return fmt.Errorf("endpoint %s/%s: %w", listener.Service, listener.Endpoint, err)
		}
		for _, procedure := range procedures {
			served[procedure] = true
		}
		for _, service := range listener.Services {
			listed[service.FullName] = true
			if service.Interface != "" {
				published[service.FullName] = service.Interface
			} else if _, seen := servers[service.FullName]; !seen {
				servers[service.FullName] = listener.Service + "/" + listener.Endpoint
			}
		}
		if listener.Package != "" {
			packages[listener.Package] = true
		}
	}
	for _, service := range ProtobufServices(set, "") {
		if !listed[service.FullName] && !packages[servicePackage(service)] {
			continue
		}
		if identity, declared := published[service.FullName]; declared {
			if server, conflict := servers[service.FullName]; conflict {
				return fmt.Errorf("service %s is declared as interface %s, implemented by other modules, and served by endpoint %s; it is one or the other", service.FullName, identity, server)
			}
			continue
		}
		for _, procedure := range service.Procedures {
			if !served[procedure] {
				return fmt.Errorf("descriptor procedure %s is served by no listener of its descriptor; the catalog lists every method of the services it serves", procedure)
			}
		}
	}
	return nil
}

// servicePackage is the proto package of a service ProtobufServices returned.
func servicePackage(service APIContractService) string {
	return strings.TrimSuffix(service.FullName, "."+service.Name)
}
