package proto

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestMarkWellKnownTypesAsImports pins which files of a descriptor set buf is
// told not to generate for: the well-known types only. Marking the module's own
// files would generate nothing at all, and marking the other shared imports
// (google/api, buf.validate) would leave the module's bindings importing a
// package that no longer exists, since managed mode rewrites their go_package
// to the generated library's own path.
//
// A file living under google/protobuf/ but declaring the module's own package
// is the module's, not a well-known type — vendoring a copy of the well-known
// types puts real files at those paths, and the package is what tells the two
// apart.
func TestMarkWellKnownTypesAsImports(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{
				Name:    googleproto.String("google/protobuf/timestamp.proto"),
				Package: googleproto.String("google.protobuf"),
			},
			{
				Name:    googleproto.String("google/api/annotations.proto"),
				Package: googleproto.String("google.api"),
			},
			{
				Name:       googleproto.String("saas/accounts/v1/accounts.proto"),
				Package:    googleproto.String("saas.accounts.v1"),
				Dependency: []string{"google/protobuf/timestamp.proto", "google/api/annotations.proto"},
			},
			{
				Name:    googleproto.String("google/protobuf/accounts_extras.proto"),
				Package: googleproto.String("saas.accounts.v1"),
			},
		},
	}
	source, err := googleproto.Marshal(set)
	require.NoError(t, err)

	data, err := markWellKnownTypesAsImports(source)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(data, &image))

	want := map[string]bool{
		"google/protobuf/timestamp.proto":       true,
		"google/api/annotations.proto":          false,
		"saas/accounts/v1/accounts.proto":       false,
		"google/protobuf/accounts_extras.proto": false,
	}
	require.Len(t, image.GetFile(), len(want))
	for _, file := range image.GetFile() {
		marked, known := want[file.GetName()]
		require.True(t, known, "unexpected file %s", file.GetName())
		require.Equal(t, marked, isMarkedAsImport(file), "file %s", file.GetName())
	}

	// The rest of the descriptor survives the round trip: a marked file keeps
	// its identity, and an unmarked one keeps the import edges the marking is
	// meant to leave alone.
	require.Equal(t, "google.protobuf", image.GetFile()[0].GetPackage())
	require.Equal(t,
		[]string{"google/protobuf/timestamp.proto", "google/api/annotations.proto"},
		image.GetFile()[2].GetDependency())
}

// TestMarkWellKnownTypesAsImportsIsIdempotent covers the composition that
// actually happens: a caller (the CLI's `generate client`) marks its own image
// before calling GenerateClient, which marks again. A second field-8042
// submessage merges with the first and is_import=true merged onto true stays
// true, so no coordinated release between core and its callers is needed.
func TestMarkWellKnownTypesAsImportsIsIdempotent(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{
			Name:    googleproto.String("google/protobuf/timestamp.proto"),
			Package: googleproto.String("google.protobuf"),
		}},
	}
	source, err := googleproto.Marshal(set)
	require.NoError(t, err)

	once, err := markWellKnownTypesAsImports(source)
	require.NoError(t, err)
	twice, err := markWellKnownTypesAsImports(once)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(twice, &image))
	require.Len(t, image.GetFile(), 1)
	require.True(t, isMarkedAsImport(image.GetFile()[0]))
}

// TestMarkWellKnownTypesAsImportsWireFormat pins the marker's exact bytes. Every
// other assertion here is written against the same constants the marker is built
// from, so it would keep passing if those drifted from buf.alpha.image.v1's
// schema and buf silently resumed generating the well-known types.
func TestMarkWellKnownTypesAsImportsWireFormat(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{
			Name:    googleproto.String("google/protobuf/empty.proto"),
			Package: googleproto.String("google.protobuf"),
		}},
	}
	source, err := googleproto.Marshal(set)
	require.NoError(t, err)

	data, err := markWellKnownTypesAsImports(source)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(data, &image))
	require.Equal(t,
		[]byte{0xd2, 0xf6, 0x03, 0x02, 0x08, 0x01},
		[]byte(image.GetFile()[0].ProtoReflect().GetUnknown()))
}

// markWellKnownTypesAsImports rejects bytes that are not a FileDescriptorSet
// rather than passing them through: buf could not have generated from them
// either, and the caller learns which of its inputs is wrong.
func TestMarkWellKnownTypesAsImportsRejectsGarbage(t *testing.T) {
	_, err := markWellKnownTypesAsImports([]byte("not-a-descriptor-set"))
	require.Error(t, err)
}

// isMarkedAsImport reports whether file carries buf's
// ImageFileExtension.is_import, read back off the wire rather than through the
// constants the marker writes.
func isMarkedAsImport(file *descriptorpb.FileDescriptorProto) bool {
	unknown := []byte(file.ProtoReflect().GetUnknown())
	for len(unknown) > 0 {
		num, typ, n := protowire.ConsumeTag(unknown)
		if n < 0 {
			return false
		}
		unknown = unknown[n:]
		size := protowire.ConsumeFieldValue(num, typ, unknown)
		if size < 0 {
			return false
		}
		if num == bufImageFileExtensionField && typ == protowire.BytesType {
			body, _ := protowire.ConsumeBytes(unknown)
			if bytes.Contains(body, protowire.AppendVarint(protowire.AppendTag(nil, bufImageFileIsImportField, protowire.VarintType), 1)) {
				return true
			}
		}
		unknown = unknown[size:]
	}
	return false
}
