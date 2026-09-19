package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/codefly-dev/core/runnable"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const BehavioralContractsFileName = "contracts/behavior.codefly.json"

// BuildPackageContractSnapshot derives evidence from the same packaged sources
// used to generate clients. A release's asserted item digests are never inputs.
func BuildPackageContractSnapshot(root string) (*updatev0.ContractSnapshot, error) {
	manifest, err := LoadPackageManifest(root)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadAPIContractCatalog(root)
	if err != nil {
		return nil, err
	}
	if catalog.Package != manifest.ID || catalog.Version != manifest.Version {
		return nil, fmt.Errorf("API catalog does not identify the module package")
	}
	if err := ValidatePackageAPIContracts(root, manifest, catalog); err != nil {
		return nil, err
	}
	for _, service := range manifest.Services {
		if len(service.Endpoints) != len(service.APIContracts) {
			return nil, fmt.Errorf("service %s has endpoints without contract sources", service.Name)
		}
	}
	snapshot := &updatev0.ContractSnapshot{SchemaVersion: 1, Module: manifest.ID, Version: manifest.Version, Complete: true}
	for _, endpoint := range catalog.Endpoints {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(endpoint.Path)))
		if err != nil {
			return nil, err
		}
		prefix := "api/" + endpoint.Service + "/" + endpoint.Endpoint
		context, err := json.Marshal([]string{endpoint.API, endpoint.Kind, endpoint.Package})
		if err != nil {
			return nil, err
		}
		items := []*updatev0.ContractItem{{Id: prefix, Digest: APIContractDigest(context)}}
		switch endpoint.Kind {
		case APIContractKindProtobuf:
			err = protobufContractItems(endpoint, prefix, data, &items)
		case APIContractKindOpenAPI:
			err = openAPIContractItems(endpoint, prefix, data, &items)
		}
		if err != nil {
			return nil, fmt.Errorf("derive %s: %w", prefix, err)
		}
		snapshot.Items = append(snapshot.Items, items...)
	}
	data, err := os.ReadFile(filepath.Join(root, BehavioralContractsFileName))
	if err != nil {
		return nil, fmt.Errorf("read behavioral contracts: %w", err)
	}
	behavior := new(updatev0.BehavioralContracts)
	if err := protojson.Unmarshal(data, behavior); err != nil {
		return nil, err
	}
	if behavior.SchemaVersion != 1 || !behavior.Complete {
		return nil, fmt.Errorf("behavioral contract source requires schema version 1 and complete coverage")
	}
	for _, contract := range behavior.Contracts {
		if contract.GetId() == "" || contract.Contract == nil {
			return nil, fmt.Errorf("behavioral contract requires an identity and canonical content")
		}
		data, err := runnable.CanonicalJSON(contract.Contract)
		if err != nil {
			return nil, err
		}
		snapshot.Items = append(snapshot.Items, &updatev0.ContractItem{
			Id: "behavior/" + contract.Id, Digest: APIContractDigest(data), Dependencies: contract.Dependencies,
			RequiredByAll: contract.RequiredByAll, Documentation: contract.Documentation,
		})
	}
	// Package-level compatibility requirements apply without a particular SDK.
	requirements, err := json.Marshal(manifest.Contracts)
	if err != nil {
		return nil, err
	}
	snapshot.Items = append(snapshot.Items, &updatev0.ContractItem{Id: "package/contracts", Digest: APIContractDigest(requirements), RequiredByAll: true})
	return moduleupdate.PrepareSnapshot(snapshot)
}

func protobufContractItems(endpoint APIContractEndpoint, prefix string, data []byte, items *[]*updatev0.ContractItem) error {
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(data, set); err != nil {
		return err
	}
	actual := ProtobufServices(set, endpoint.Package)
	expected := slices.Clone(endpoint.Services)
	for i := range expected {
		expected[i].Procedures = slices.Clone(expected[i].Procedures)
		slices.Sort(expected[i].Procedures)
	}
	slices.SortFunc(expected, func(a, b APIContractService) int { return strings.Compare(a.FullName, b.FullName) })
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("catalog procedures do not match the descriptor set")
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return err
	}
	seen := make(map[protoreflect.FullName]bool)
	var addType func(protoreflect.Descriptor) error
	addType = func(descriptor protoreflect.Descriptor) error {
		if seen[descriptor.FullName()] {
			return nil
		}
		seen[descriptor.FullName()] = true
		content := contractFileContext(descriptor.ParentFile())
		item := &updatev0.ContractItem{Id: prefix + "#" + string(descriptor.FullName())}
		switch typed := descriptor.(type) {
		case protoreflect.MessageDescriptor:
			content.MessageType = []*descriptorpb.DescriptorProto{protodesc.ToDescriptorProto(typed)}
			for i := 0; i < typed.Fields().Len(); i++ {
				field := typed.Fields().Get(i)
				var dependency protoreflect.Descriptor
				if field.Message() != nil {
					dependency = field.Message()
				} else if field.Enum() != nil {
					dependency = field.Enum()
				}
				if dependency != nil {
					item.Dependencies = append(item.Dependencies, prefix+"#"+string(dependency.FullName()))
					if err := addType(dependency); err != nil {
						return err
					}
				}
			}
		case protoreflect.EnumDescriptor:
			content.EnumType = []*descriptorpb.EnumDescriptorProto{protodesc.ToEnumDescriptorProto(typed)}
		}
		canonical, err := runnable.CanonicalJSON(content)
		if err != nil {
			return err
		}
		item.Digest = APIContractDigest(canonical)
		slices.Sort(item.Dependencies)
		item.Dependencies = slices.Compact(item.Dependencies)
		*items = append(*items, item)
		return nil
	}
	for _, service := range actual {
		descriptor, err := files.FindDescriptorByName(protoreflect.FullName(service.FullName))
		if err != nil {
			return err
		}
		svc := descriptor.(protoreflect.ServiceDescriptor)
		for i := 0; i < svc.Methods().Len(); i++ {
			method := svc.Methods().Get(i)
			for _, message := range []protoreflect.MessageDescriptor{method.Input(), method.Output()} {
				if err := addType(message); err != nil {
					return err
				}
			}
			file := contractFileContext(svc.ParentFile())
			file.Service = []*descriptorpb.ServiceDescriptorProto{{Name: proto.String(string(svc.Name())), Options: protodesc.ToServiceDescriptorProto(svc).Options, Method: []*descriptorpb.MethodDescriptorProto{protodesc.ToMethodDescriptorProto(method)}}}
			content, err := runnable.CanonicalJSON(file)
			if err != nil {
				return err
			}
			dependencies := []string{prefix, prefix + "#" + string(method.Input().FullName()), prefix + "#" + string(method.Output().FullName())}
			slices.Sort(dependencies)
			*items = append(*items, &updatev0.ContractItem{Id: prefix + "#/" + service.FullName + "/" + string(method.Name()), Digest: APIContractDigest(content), Dependencies: slices.Compact(dependencies)})
		}
	}
	return nil
}

func contractFileContext(descriptor protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	file := protodesc.ToFileDescriptorProto(descriptor)
	file.MessageType, file.EnumType, file.Service, file.SourceCodeInfo = nil, nil, nil, nil
	file.Dependency, file.PublicDependency, file.WeakDependency = nil, nil, nil
	return file
}

var openAPIMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func openAPIContractItems(endpoint APIContractEndpoint, prefix string, data []byte, items *[]*updatev0.ContractItem) error {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("OpenAPI source has trailing JSON content")
	}
	version, _ := document["openapi"].(string)
	if document["swagger"] != "2.0" && !strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.") {
		return fmt.Errorf("unsupported OpenAPI version")
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return fmt.Errorf("OpenAPI paths must be an object")
	}
	seen := make(map[string]bool)
	var refs func(any) ([]string, error)
	refs = func(value any) ([]string, error) {
		var dependencies []string
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/") {
						return nil, fmt.Errorf("unsupported external or malformed reference %v", child)
					}
					dependencies = append(dependencies, prefix+ref)
					if seen[ref] {
						continue
					}
					seen[ref] = true
					var target any = document
					for _, part := range strings.Split(ref[2:], "/") {
						object, ok := target.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("unresolved reference %s", ref)
						}
						target, ok = object[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
						if !ok {
							return nil, fmt.Errorf("unresolved reference %s", ref)
						}
					}
					nested, err := refs(target)
					if err != nil {
						return nil, err
					}
					canonical, err := json.Marshal(target)
					if err != nil {
						return nil, err
					}
					*items = append(*items, &updatev0.ContractItem{Id: prefix + ref, Digest: APIContractDigest(canonical), Dependencies: nested})
				} else {
					nested, err := refs(child)
					if err != nil {
						return nil, err
					}
					dependencies = append(dependencies, nested...)
				}
			}
		case []any:
			for _, child := range node {
				nested, err := refs(child)
				if err != nil {
					return nil, err
				}
				dependencies = append(dependencies, nested...)
			}
		}
		slices.Sort(dependencies)
		return slices.Compact(dependencies), nil
	}
	globals := make(map[string]any)
	for key, value := range document {
		if !slices.Contains([]string{"paths", "components", "definitions", "parameters", "responses", "info", "tags", "externalDocs"}, key) {
			globals[key] = value
		}
	}
	if components, ok := document["components"].(map[string]any); ok {
		globals["securitySchemes"] = components["securitySchemes"]
	}
	globalDeps, err := refs(globals)
	if err != nil {
		return err
	}
	globalJSON, err := json.Marshal(globals)
	if err != nil {
		return err
	}
	*items = append(*items, &updatev0.ContractItem{Id: prefix + "#context", Digest: APIContractDigest(globalJSON), Dependencies: globalDeps})
	var routes []APIContractRoute
	for path, raw := range paths {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok || !strings.HasPrefix(path, "/") || entry["$ref"] != nil {
			return fmt.Errorf("unsupported or malformed path item %s", path)
		}
		context := make(map[string]any)
		for key, value := range entry {
			if !slices.Contains(openAPIMethods, key) {
				context[key] = value
			}
		}
		for _, method := range openAPIMethods {
			operation, exists := entry[method]
			if !exists {
				continue
			}
			if _, ok := operation.(map[string]any); !ok {
				return fmt.Errorf("malformed operation %s %s", method, path)
			}
			content := []any{context, operation}
			dependencies, err := refs(content)
			if err != nil {
				return err
			}
			canonical, err := json.Marshal(content)
			if err != nil {
				return err
			}
			routes = append(routes, APIContractRoute{Method: strings.ToUpper(method), Path: path})
			*items = append(*items, &updatev0.ContractItem{Id: prefix + "#" + strings.ToUpper(method) + " " + path, Digest: APIContractDigest(canonical), Dependencies: append(dependencies, prefix, prefix+"#context")})
		}
	}
	expected := slices.Clone(endpoint.Routes)
	order := func(a, b APIContractRoute) int { return strings.Compare(a.Method+" "+a.Path, b.Method+" "+b.Path) }
	slices.SortFunc(routes, order)
	slices.SortFunc(expected, order)
	if !slices.Equal(routes, expected) {
		return fmt.Errorf("catalog routes do not match the OpenAPI document")
	}
	return nil
}
