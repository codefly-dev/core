package runnable_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const (
	tString   = descriptorpb.FieldDescriptorProto_TYPE_STRING
	tBool     = descriptorpb.FieldDescriptorProto_TYPE_BOOL
	tInt32    = descriptorpb.FieldDescriptorProto_TYPE_INT32
	tInt64    = descriptorpb.FieldDescriptorProto_TYPE_INT64
	tSint32   = descriptorpb.FieldDescriptorProto_TYPE_SINT32
	tSint64   = descriptorpb.FieldDescriptorProto_TYPE_SINT64
	tSfixed32 = descriptorpb.FieldDescriptorProto_TYPE_SFIXED32
	tSfixed64 = descriptorpb.FieldDescriptorProto_TYPE_SFIXED64
	tUint32   = descriptorpb.FieldDescriptorProto_TYPE_UINT32
	tUint64   = descriptorpb.FieldDescriptorProto_TYPE_UINT64
	tFixed32  = descriptorpb.FieldDescriptorProto_TYPE_FIXED32
	tFixed64  = descriptorpb.FieldDescriptorProto_TYPE_FIXED64
	tBytes    = descriptorpb.FieldDescriptorProto_TYPE_BYTES
	tFloat    = descriptorpb.FieldDescriptorProto_TYPE_FLOAT
	tDouble   = descriptorpb.FieldDescriptorProto_TYPE_DOUBLE
	tEnum     = descriptorpb.FieldDescriptorProto_TYPE_ENUM
	tMessage  = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
)

func projected(t *testing.T, spec fileSpec, name string) (*basev0.RunnableSchema, error) {
	t.Helper()
	file := compile(t, spec)
	md := file.Messages().ByName(protoreflect.Name(name))
	require.NotNil(t, md)
	return runnable.ProjectMessage(md)
}

func TestProjectMessageCoversTheBoundedProfile(t *testing.T) {
	inner := message("Inner", scalar("label", 1, tString))
	root := message("Root",
		scalar("text", 1, tString),
		scalar("flag", 2, tBool),
		scalar("i32", 3, tInt32),
		scalar("i64", 4, tInt64),
		scalar("s32", 5, tSint32),
		scalar("s64", 6, tSint64),
		scalar("sf32", 7, tSfixed32),
		scalar("sf64", 8, tSfixed64),
		scalar("u32", 9, tUint32),
		scalar("f32", 10, tFixed32),
		named("nested", 11, tMessage, ".fixture.Inner"),
		repeated(scalar("tags", 12, tString)),
		repeated(named("many", 13, tMessage, ".fixture.Inner")),
		presence(scalar("note", 14, tString), 0),
	)
	root.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_note")}}

	schema, err := projected(t, fileSpec{name: "fixture.proto", pkg: "fixture", messages: []*descriptorpb.DescriptorProto{inner, root}}, "Root")
	require.NoError(t, err)

	object := &basev0.RunnableField{Name: "nested", Type: basev0.RunnableField_OBJECT, Optional: true,
		Fields: []*basev0.RunnableField{{Name: "label", Type: basev0.RunnableField_STRING}}}
	require.True(t, proto.Equal(&basev0.RunnableSchema{Fields: []*basev0.RunnableField{
		{Name: "text", Type: basev0.RunnableField_STRING},
		{Name: "flag", Type: basev0.RunnableField_BOOLEAN},
		{Name: "i32", Type: basev0.RunnableField_INTEGER},
		{Name: "i64", Type: basev0.RunnableField_INTEGER},
		{Name: "s32", Type: basev0.RunnableField_INTEGER},
		{Name: "s64", Type: basev0.RunnableField_INTEGER},
		{Name: "sf32", Type: basev0.RunnableField_INTEGER},
		{Name: "sf64", Type: basev0.RunnableField_INTEGER},
		{Name: "u32", Type: basev0.RunnableField_INTEGER},
		{Name: "f32", Type: basev0.RunnableField_INTEGER},
		object,
		// An array's items carry neither the field's name nor its optionality.
		{Name: "tags", Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_STRING}},
		{Name: "many", Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_OBJECT,
			Fields: []*basev0.RunnableField{{Name: "label", Type: basev0.RunnableField_STRING}}}},
		{Name: "note", Type: basev0.RunnableField_STRING, Optional: true},
	}}, schema), "projected %v", schema)

	// A projection is only useful if the contract it lands in loads, so the
	// profile's own rules are applied to it rather than assumed.
	contract, err := resources.RunnableContractFromProto(&basev0.RunnableContract{
		Protocol: resources.RunnableServiceProtocolV1, Input: schema, Output: schema})
	require.NoError(t, err)
	require.NoError(t, contract.Validate())
}

func TestProjectMessageProjectsAnEmptyMessage(t *testing.T) {
	schema, err := projected(t, fileSpec{name: "empty.proto", pkg: "fixture",
		messages: []*descriptorpb.DescriptorProto{message("Root")}}, "Root")
	require.NoError(t, err)
	require.Empty(t, schema.GetFields())
}

func TestProjectMessageRejectsEveryTypeOutsideTheProfile(t *testing.T) {
	enum := &descriptorpb.EnumDescriptorProto{Name: proto.String("Colour"),
		Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("COLOUR_UNKNOWN"), Number: proto.Int32(0)}}}

	oneof := message("Root", inOneof(scalar("choice", 1, tString), 0))
	oneof.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("pick")}}

	mapped := message("Root")
	mapField("fixture", mapped, "labels", 1)

	nested := message("Root", named("inner", 1, tMessage, ".fixture.Inner"))
	inner := message("Inner", scalar("weight", 1, tDouble))

	for _, test := range []struct {
		name    string
		spec    fileSpec
		path    string
		because string
	}{
		{"uint64", fileSpec{messages: []*descriptorpb.DescriptorProto{message("Root", scalar("count", 1, tUint64))}}, "fixture.Root.count", "exceeds the signed 64-bit"},
		{"fixed64", fileSpec{messages: []*descriptorpb.DescriptorProto{message("Root", scalar("count", 1, tFixed64))}}, "fixture.Root.count", "exceeds the signed 64-bit"},
		{"bytes", fileSpec{messages: []*descriptorpb.DescriptorProto{message("Root", scalar("blob", 1, tBytes))}}, "fixture.Root.blob", "does not cover"},
		{"float", fileSpec{messages: []*descriptorpb.DescriptorProto{message("Root", scalar("ratio", 1, tFloat))}}, "fixture.Root.ratio", "does not cover"},
		{"double", fileSpec{messages: []*descriptorpb.DescriptorProto{message("Root", scalar("ratio", 1, tDouble))}}, "fixture.Root.ratio", "does not cover"},
		{"enum", fileSpec{enums: []*descriptorpb.EnumDescriptorProto{enum}, messages: []*descriptorpb.DescriptorProto{message("Root", named("colour", 1, tEnum, ".fixture.Colour"))}}, "fixture.Root.colour", "does not cover"},
		{"map", fileSpec{messages: []*descriptorpb.DescriptorProto{mapped}}, "fixture.Root.labels", "is a map"},
		{"oneof", fileSpec{messages: []*descriptorpb.DescriptorProto{oneof}}, "fixture.Root.choice", "belongs to oneof pick"},
		{"well-known type", fileSpec{imports: []string{"google/protobuf/timestamp.proto"}, messages: []*descriptorpb.DescriptorProto{message("Root", named("at", 1, tMessage, ".google.protobuf.Timestamp"))}}, "fixture.Root.at", "well-known type"},
		{"any", fileSpec{imports: []string{"google/protobuf/any.proto"}, messages: []*descriptorpb.DescriptorProto{message("Root", named("payload", 1, tMessage, ".google.protobuf.Any"))}}, "fixture.Root.payload", "well-known type"},
		// A nested rejection is named by the whole path, not by the leaf: the
		// same message may be reached from several fields.
		{"nested", fileSpec{messages: []*descriptorpb.DescriptorProto{nested, inner}}, "fixture.Root.inner.weight", "does not cover"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := test.spec
			spec.name, spec.pkg = test.name+".proto", "fixture"
			_, err := projected(t, spec, "Root")
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.path)
			require.ErrorContains(t, err, test.because)
		})
	}
}

func TestProjectMessageRejectsRecursionAndExcessiveDepth(t *testing.T) {
	_, err := projected(t, fileSpec{name: "cycle.proto", pkg: "fixture", messages: []*descriptorpb.DescriptorProto{
		message("Root", named("child", 1, tMessage, ".fixture.Root")),
	}}, "Root")
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "reaches fixture.Root again")

	// A cycle through a second message is the same unbounded payload.
	_, err = projected(t, fileSpec{name: "mutual.proto", pkg: "fixture", messages: []*descriptorpb.DescriptorProto{
		message("Root", named("other", 1, tMessage, ".fixture.Other")),
		message("Other", named("back", 1, tMessage, ".fixture.Root")),
	}}, "Root")
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "reaches fixture.Root again")

	chain := func(length int) fileSpec {
		messages := make([]*descriptorpb.DescriptorProto, 0, length)
		for i := range length {
			last := i == length-1
			if last {
				messages = append(messages, message(fmt.Sprintf("M%d", i), scalar("leaf", 1, tString)))
				continue
			}
			messages = append(messages, message(fmt.Sprintf("M%d", i), named("next", 1, tMessage, fmt.Sprintf(".fixture.M%d", i+1))))
		}
		return fileSpec{name: "chain.proto", pkg: "fixture", messages: messages}
	}
	_, err = projected(t, chain(runnable.MaxProjectionDepth), "M0")
	require.NoError(t, err)
	_, err = projected(t, chain(runnable.MaxProjectionDepth+1), "M0")
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, fmt.Sprintf("nests more than %d messages deep", runnable.MaxProjectionDepth))
}

func TestProjectMessageRequiresADescriptor(t *testing.T) {
	_, err := runnable.ProjectMessage(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

// TestProjectMessageRejectsAWellKnownPayload covers the root message, which the
// per-field guard never sees: google.protobuf.Timestamp projects into two
// integers just as happily at the payload as nested, and the wrappers project
// into a single-key object.
func TestProjectMessageRejectsAWellKnownPayload(t *testing.T) {
	for _, wellKnown := range []protoreflect.MessageDescriptor{
		(&timestamppb.Timestamp{}).ProtoReflect().Descriptor(),
		(&durationpb.Duration{}).ProtoReflect().Descriptor(),
		(&emptypb.Empty{}).ProtoReflect().Descriptor(),
		(&wrapperspb.StringValue{}).ProtoReflect().Descriptor(),
		(&wrapperspb.BoolValue{}).ProtoReflect().Descriptor(),
		(&wrapperspb.Int64Value{}).ProtoReflect().Descriptor(),
	} {
		t.Run(string(wellKnown.FullName()), func(t *testing.T) {
			_, err := runnable.ProjectMessage(wellKnown)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, "payload "+string(wellKnown.FullName()))
		})
	}
}

// TestProjectMessageKeepsARequiredKeyPresent guards the inverse reading of
// presence: proto2 required and editions LEGACY_REQUIRED track presence, but
// their key is never absent.
func TestProjectMessageKeepsARequiredKeyPresent(t *testing.T) {
	required := scalar("id", 1, tString)
	required.Label = descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum()
	fd := fileSpec{name: "legacy.proto", pkg: "legacy", messages: []*descriptorpb.DescriptorProto{
		message("Root", required, scalar("note", 2, tString)),
	}}.proto()
	fd.Syntax = proto.String("proto2")
	file, err := protodesc.NewFile(fd, protoregistry.GlobalFiles)
	require.NoError(t, err)

	schema, err := runnable.ProjectMessage(file.Messages().ByName("Root"))
	require.NoError(t, err)
	require.True(t, proto.Equal(&basev0.RunnableSchema{Fields: []*basev0.RunnableField{
		{Name: "id", Type: basev0.RunnableField_STRING},
		// proto2 optional does have an absent key.
		{Name: "note", Type: basev0.RunnableField_STRING, Optional: true},
	}}, schema), "projected %v", schema)
}
