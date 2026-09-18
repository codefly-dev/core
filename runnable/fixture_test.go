package runnable_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// The projection and the package builder read descriptors, so the fixtures are
// descriptors. Building them here rather than checking in a compiled
// descriptor set keeps the rejection table exhaustive — a map, a oneof and a
// uint64 field are three lines each — and keeps the suite free of a generated
// artifact nothing regenerates.

func scalar(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Type:   kind.Enum(),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
}

func named(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
	field := scalar(name, number, kind)
	field.TypeName = proto.String(typeName)
	return field
}

func repeated(field *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return field
}

// presence makes a proto3 field explicitly optional, which the wire format
// spells as membership of a synthetic one-member oneof.
func presence(field *descriptorpb.FieldDescriptorProto, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	field.Proto3Optional = proto.Bool(true)
	field.OneofIndex = proto.Int32(oneofIndex)
	return field
}

func inOneof(field *descriptorpb.FieldDescriptorProto, oneofIndex int32) *descriptorpb.FieldDescriptorProto {
	field.OneofIndex = proto.Int32(oneofIndex)
	return field
}

func message(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: fields}
}

// mapField is the pair protoc generates for a map<string, string>: a repeated
// field of a nested entry message marked map_entry.
func mapField(pkg string, parent *descriptorpb.DescriptorProto, name string, number int32) {
	// protodesc insists an implicit map entry is named after its field.
	entry := strings.ToUpper(name[:1]) + name[1:] + "Entry"
	parent.NestedType = append(parent.NestedType, &descriptorpb.DescriptorProto{
		Name:    proto.String(entry),
		Field:   []*descriptorpb.FieldDescriptorProto{scalar("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING), scalar("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING)},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	})
	parent.Field = append(parent.Field, repeated(named(name, number, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, "."+pkg+"."+*parent.Name+"."+entry)))
}

type fileSpec struct {
	name     string
	pkg      string
	imports  []string
	messages []*descriptorpb.DescriptorProto
	enums    []*descriptorpb.EnumDescriptorProto
	services []*descriptorpb.ServiceDescriptorProto
}

func (f fileSpec) proto() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:        proto.String(f.name),
		Package:     proto.String(f.pkg),
		Syntax:      proto.String("proto3"),
		Dependency:  f.imports,
		MessageType: f.messages,
		EnumType:    f.enums,
		Service:     f.services,
	}
}

// compile resolves the fixture against the linked-in descriptors, so an import
// of a well-known type or of the operation option needs no second copy here.
func compile(t *testing.T, spec fileSpec) protoreflect.FileDescriptor {
	t.Helper()
	file, err := protodesc.NewFile(spec.proto(), protoregistry.GlobalFiles)
	require.NoError(t, err)
	return file
}

func registry(t *testing.T, file protoreflect.FileDescriptor) *protoregistry.Files {
	t.Helper()
	files := &protoregistry.Files{}
	require.NoError(t, files.RegisterFile(file))
	return files
}

// operationMethod is one unary rpc carrying the operation option.
func operationMethod(name, input, output string, declared *runnablev0.Operation) *descriptorpb.MethodDescriptorProto {
	method := &descriptorpb.MethodDescriptorProto{
		Name:       proto.String(name),
		InputType:  proto.String(input),
		OutputType: proto.String(output),
	}
	if declared != nil {
		method.Options = &descriptorpb.MethodOptions{}
		proto.SetExtension(method.Options, runnablev0.E_Operation, declared)
	}
	return method
}

func service(name string, methods ...*descriptorpb.MethodDescriptorProto) *descriptorpb.ServiceDescriptorProto {
	return &descriptorpb.ServiceDescriptorProto{Name: proto.String(name), Method: methods}
}
