// Package runnablefixture builds a workspace's runnable-bindings group the
// way `codefly generate runnable-bindings` writes it, at production sizes, so
// core's delivery tests exercise the limits real workspaces hit, and installs
// it the way a worker does.
//
// Every operation is derived for real: each owner endpoint publishes a
// service whose methods carry the operation option, core derives each
// method's package (runnable.PackageFromMethod) and prepares its SERVICE
// binding (runnable.PrepareBinding). The owner file imports one of core's own
// large service contracts, so the published contract — with SourceCodeInfo,
// as `codefly generate contracts` leaves it — is of the size real owners
// have, not padding.
package runnablefixture

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Group is the workspace configuration group prepared bindings are written to.
const Group = "runnable-bindings"

// Endpoint is one owner endpoint: its published contract, as `codefly
// generate contracts` writes it, and the lean set delivered for it.
type Endpoint struct {
	Service   protoreflect.FullName
	Contract  []byte
	Set       []byte
	Reference *runnable.DescriptorSetReference
}

// Operation is one prepared operation.
type Operation struct {
	Key      string
	Method   string
	Endpoint *Endpoint
	Value    string
}

// Workspace is a workspace whose modules derived Operations operations over
// Endpoints owner endpoints.
type Workspace struct {
	Endpoints  []*Endpoint
	Operations []*Operation
}

// large are the contracts an owner file imports so that its published
// contract is of the size a real owner's is.
var large = []protoreflect.FileDescriptor{
	builderv0.File_codefly_services_builder_v0_builder_proto,
	runtimev0.File_codefly_services_runtime_v0_runtime_proto,
}

// Build derives operations operations spread round-robin over endpoints owner
// endpoints (at most len(large)), and prepares each for the local
// environment.
func Build(operations, endpoints int) (*Workspace, error) {
	if endpoints < 1 || endpoints > len(large) || operations < 1 {
		return nil, fmt.Errorf("runnablefixture: %d operations over %d endpoints", operations, endpoints)
	}
	perEndpoint := make([]int, endpoints)
	for i := 0; i < operations; i++ {
		perEndpoint[i%endpoints]++
	}
	workspace := &Workspace{}
	registries := make([]*protoregistry.Files, endpoints)
	for i := 0; i < endpoints; i++ {
		owner, err := ownerFile(i, perEndpoint[i])
		if err != nil {
			return nil, err
		}
		files := &protoregistry.Files{}
		if err = files.RegisterFile(owner); err != nil {
			return nil, err
		}
		registries[i] = files
		contract, err := publishedContract(owner)
		if err != nil {
			return nil, err
		}
		set, err := runnable.LeanDescriptorSet(contract)
		if err != nil {
			return nil, err
		}
		workspace.Endpoints = append(workspace.Endpoints, &Endpoint{
			Service:   owner.Services().Get(0).FullName(),
			Contract:  contract,
			Set:       set,
			Reference: &runnable.DescriptorSetReference{Digest: runnable.DescriptorSetDigest(set), Contract: runnable.DescriptorSetDigest(contract)},
		})
	}
	for i := 0; i < operations; i++ {
		endpoint := workspace.Endpoints[i%endpoints]
		method := fmt.Sprintf("/%s/Operation%02d", endpoint.Service, i/endpoints)
		key := fmt.Sprintf("ACME__OPERATION_%02d", i)
		value, err := preparedValue(registries[i%endpoints], i%endpoints, i, method, endpoint.Reference)
		if err != nil {
			return nil, fmt.Errorf("runnablefixture: %s: %w", key, err)
		}
		workspace.Operations = append(workspace.Operations, &Operation{Key: key, Method: method, Endpoint: endpoint, Value: value})
	}
	return workspace, nil
}

// Configuration is the runnable-bindings group as the workspace delivers it:
// every prepared value, and each referenced descriptor set once.
func (w *Workspace) Configuration() *basev0.Configuration {
	info := &basev0.ConfigurationInformation{Name: Group}
	for _, operation := range w.Operations {
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: operation.Key, Value: operation.Value})
	}
	for _, endpoint := range w.Endpoints {
		key, _ := endpoint.Reference.Key()
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: key, Value: runnable.EncodeDescriptorSet(endpoint.Set)})
	}
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}
}

// Embedded is the same group in the form that preceded sharing: each value
// carrying its own copy of its owner's descriptors, with source info removed.
func (w *Workspace) Embedded() *basev0.Configuration {
	info := &basev0.ConfigurationInformation{Name: Group}
	for _, operation := range w.Operations {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(operation.Value), &fields)
		delete(fields, "schema")
		delete(fields, "descriptor_set")
		fields["descriptors"], _ = json.Marshal(base64.StdEncoding.EncodeToString(operation.Endpoint.Set))
		embedded, _ := json.Marshal(fields)
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{Key: operation.Key, Value: string(embedded)})
	}
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}
}

// Installed is what a worker holds once it has installed one prepared value.
type Installed struct {
	Package    *basev0.RunnablePackage
	Binding    *basev0.RunnableBinding
	Method     protoreflect.MethodDescriptor
	SetDigest  string
	Descriptor *descriptorpb.FileDescriptorSet
}

// Install is a worker's installation of one runnable-bindings value, read
// through lookup (a workspace value lookup in Group): decode it strictly,
// verify its package and its binding against that package, resolve its
// owner's descriptor set by digest, and find the operation's method, with the
// messages the package names, in those descriptors.
func Install(value string, lookup func(key string) (string, error)) (*Installed, error) {
	prepared, err := runnable.DecodePrepared([]byte(value))
	if err != nil {
		return nil, err
	}
	pkg := &basev0.RunnablePackage{}
	if err = protojson.Unmarshal(prepared.Package, pkg); err != nil {
		return nil, fmt.Errorf("package: %w", err)
	}
	if err = runnable.VerifyPackage(pkg); err != nil {
		return nil, err
	}
	binding := &basev0.RunnableBinding{}
	if err = protojson.Unmarshal(prepared.Binding, binding); err != nil {
		return nil, fmt.Errorf("binding: %w", err)
	}
	if err = runnable.VerifyBinding(binding, pkg); err != nil {
		return nil, err
	}
	set, err := runnable.ResolveDescriptorSet(prepared.DescriptorSet, lookup)
	if err != nil {
		return nil, err
	}
	descriptors := &descriptorpb.FileDescriptorSet{}
	if err = proto.Unmarshal(set, descriptors); err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(descriptors)
	if err != nil {
		return nil, fmt.Errorf("owner descriptors: %w", err)
	}
	operation := binding.GetServiceOperation()
	service, name, _ := strings.Cut(strings.TrimPrefix(operation.GetOperation(), "/"), "/")
	found, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("owner descriptors: %w", err)
	}
	published, ok := found.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("owner descriptors: %s is not a service", service)
	}
	method := published.Methods().ByName(protoreflect.Name(name))
	if method == nil {
		return nil, fmt.Errorf("method %s is not in its owner's descriptors", operation.GetOperation())
	}
	if string(method.Input().FullName()) != operation.GetInputMessage() || string(method.Output().FullName()) != operation.GetOutputMessage() {
		return nil, fmt.Errorf("method %s does not carry the messages its package names", operation.GetOperation())
	}
	return &Installed{Package: pkg, Binding: binding, Method: method, SetDigest: prepared.DescriptorSet.Digest, Descriptor: descriptors}, nil
}

// ownerFile is owner endpoint index's published service: operations unary
// methods marked as operations, in a file importing a large real contract.
func ownerFile(index, operations int) (protoreflect.FileDescriptor, error) {
	pkg := fmt.Sprintf("acme.owner%d.v1", index)
	imported := large[index]
	request := &descriptorpb.DescriptorProto{Name: proto.String("Request"), Field: []*descriptorpb.FieldDescriptorProto{
		field("item", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		field("revision", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
	}}
	response := &descriptorpb.DescriptorProto{Name: proto.String("Response"), Field: []*descriptorpb.FieldDescriptorProto{
		field("reference", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		field("applied", 2, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
	}}
	// The owner's other surface uses the imported contract, as a real
	// service's does; a caller resolving an operation never reads it.
	var surface *descriptorpb.DescriptorProto
	for i := 0; i < imported.Messages().Len(); i++ {
		if message := imported.Messages().Get(i); !message.IsMapEntry() {
			surface = &descriptorpb.DescriptorProto{Name: proto.String("Surface"), Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("value"), Number: proto.Int32(1), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String("." + string(message.FullName())),
			}}}
			break
		}
	}
	service := &descriptorpb.ServiceDescriptorProto{Name: proto.String("Items")}
	for i := 0; i < operations; i++ {
		options := &descriptorpb.MethodOptions{}
		proto.SetExtension(options, runnablev0.E_Operation, &runnablev0.Operation{
			AttemptTimeout: durationpb.New(10 * time.Second),
			TotalTimeout:   durationpb.New(time.Minute),
			MaxAttempts:    3,
			Backoff:        durationpb.New(time.Second),
			RetryableCodes: []string{"UNAVAILABLE"},
			Audience:       "acme.items",
			InvokeScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read", "write"}}},
			LookupScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read"}}},
		})
		service.Method = append(service.Method, &descriptorpb.MethodDescriptorProto{
			Name: proto.String(fmt.Sprintf("Operation%02d", i)), InputType: proto.String("." + pkg + ".Request"),
			OutputType: proto.String("." + pkg + ".Response"), Options: options,
		})
	}
	return protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:        proto.String(fmt.Sprintf("acme/owner%d/v1/items.proto", index)),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		Dependency:  []string{"codefly/runnable/v0/options.proto", imported.Path()},
		MessageType: []*descriptorpb.DescriptorProto{request, response, surface},
		Service:     []*descriptorpb.ServiceDescriptorProto{service},
	}, protoregistry.GlobalFiles)
}

func field(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(number), Type: kind.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()}
}

// preparedValue derives method's package, prepares its SERVICE binding on
// the owner endpoint at a local address, and writes the prepared value the
// way `codefly generate runnable-bindings` does.
func preparedValue(files *protoregistry.Files, endpoint, index int, method string, reference *runnable.DescriptorSetReference) (string, error) {
	module, service := fmt.Sprintf("owner%d", endpoint), "items"
	location := &resources.RunnableLocation{
		Identity:            &resources.RunnableIdentity{Name: fmt.Sprintf("operation-%02d", index), Module: module, Workspace: "example", Version: "0.1.0"},
		WorkspacePath:       "/workspaces/example",
		RelativeToWorkspace: "modules/" + module,
	}
	owner := runnable.ServiceOwner{Module: module, Service: service, Endpoint: "grpc",
		Agent: &basev0.Agent{Kind: basev0.Agent_SERVICE, Name: "go-grpc", Version: "0.1.48", Publisher: "codefly.dev"}}
	pkg, spec, err := runnable.PackageFromMethod(files, location, owner, method)
	if err != nil {
		return "", err
	}
	binding, err := runnable.PrepareBinding(&basev0.RunnableBinding{
		Schema:         runnable.BindingSchemaV1,
		Identity:       pkg.GetIdentity(),
		PackageDigest:  pkg.GetDigest(),
		Facility:       &basev0.RunnableFacility{Kind: basev0.RunnableFacility_SERVICE},
		Implementation: &basev0.RunnableBinding_ServiceOperation{ServiceOperation: pkg.GetServiceOperations()[0]},
		Target: &basev0.RunnableTarget{
			Schema: runnable.TargetSchemaV1, Environment: "local", Revision: fmt.Sprintf("install-%02d", index),
			Coordinates: &basev0.RunnableTarget_Service{Service: &basev0.RunnableServiceTarget{Endpoint: &basev0.NetworkMapping{
				Endpoint:  &basev0.Endpoint{Name: "grpc", Service: service, Module: module, Api: "grpc", Visibility: "module"},
				Instances: []*basev0.NetworkInstance{{Address: fmt.Sprintf("localhost:%d", 20000+endpoint)}},
			}}},
		},
	}, pkg)
	if err != nil {
		return "", err
	}
	packageJSON, err := runnable.CanonicalJSON(pkg)
	if err != nil {
		return "", err
	}
	bindingJSON, err := runnable.CanonicalJSON(binding)
	if err != nil {
		return "", err
	}
	operation, err := json.Marshal(map[string]any{
		"method": spec.Method, "attempt_timeout": spec.AttemptTimeout.String(), "total_timeout": spec.TotalTimeout.String(),
		"max_attempts": spec.MaxAttempts, "backoff": spec.Backoff.String(), "retryable_codes": spec.RetryableCodes,
		"audience": spec.Audience, "invoke_scopes": scopes(spec.InvokeScopes), "lookup_scopes": scopes(spec.LookupScopes),
	})
	if err != nil {
		return "", err
	}
	value, err := json.Marshal(&runnable.Prepared{
		Schema: runnable.PreparedSchemaV2, Package: packageJSON, Binding: bindingJSON, Operation: operation, DescriptorSet: reference,
	})
	if err != nil {
		return "", err
	}
	if _, err = runnable.DecodePrepared(value); err != nil {
		return "", err
	}
	return string(value), nil
}

func scopes(declared []*basev0.WorkScopeV1) []map[string]any {
	out := []map[string]any{}
	for _, scope := range declared {
		out = append(out, map[string]any{"resource_kind": scope.GetResourceKind(), "actions": scope.GetActions()})
	}
	return out
}

// publishedContract is the owner file and its imports, each carrying source
// info, as a published contract.binpb does.
func publishedContract(root protoreflect.FileDescriptor) ([]byte, error) {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var visit func(protoreflect.FileDescriptor)
	visit = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		imports := file.Imports()
		for i := 0; i < imports.Len(); i++ {
			visit(imports.Get(i).FileDescriptor)
		}
		described := protodesc.ToFileDescriptorProto(file)
		described.SourceCodeInfo = &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{{
			Path: []int32{4}, Span: []int32{1, 0, 1}, LeadingComments: proto.String(strings.Repeat(" documentation a caller never reads\n", 256)),
		}}}
		set.File = append(set.File, described)
	}
	visit(root)
	return proto.MarshalOptions{Deterministic: true}.Marshal(set)
}
