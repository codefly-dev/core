package composition

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/types/descriptorpb"
)

// ProtobufProcedures validates a listener's explicit inventory against all
// packages in its descriptor. Imported schemas alone never establish serving.
func ProtobufProcedures(set *descriptorpb.FileDescriptorSet, surface []APIContractService) ([]string, error) {
	available := map[string]APIContractService{}
	for _, service := range ProtobufServices(set, "") {
		available[service.FullName] = service
	}
	seenServices, seen := map[string]bool{}, map[string]bool{}
	var methods []string
	for _, service := range surface {
		declared, exists := available[service.FullName]
		if !exists {
			return nil, fmt.Errorf("service %s is absent from its descriptor", service.FullName)
		}
		if service.Name != declared.Name || seenServices[service.FullName] {
			return nil, fmt.Errorf("service %s must use descriptor name %s and appear exactly once", service.FullName, declared.Name)
		}
		seenServices[service.FullName] = true
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
