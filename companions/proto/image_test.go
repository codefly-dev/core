package proto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/languages"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// foreignSet is the shape a codefly contract has on the wire: the module's own
// files plus every namespace the contract imports, all of them plain
// FileDescriptorProtos with no buf image extensions.
func foreignSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{
				Name:    googleproto.String("google/protobuf/timestamp.proto"),
				Package: googleproto.String("google.protobuf"),
				Options: &descriptorpb.FileOptions{
					GoPackage: googleproto.String("google.golang.org/protobuf/types/known/timestamppb"),
				},
			},
			{
				// A well-known type whose proto package is NOT "google.protobuf":
				// an exact package match leaves it a generation target.
				Name:    googleproto.String("google/protobuf/compiler/plugin.proto"),
				Package: googleproto.String("google.protobuf.compiler"),
				Options: &descriptorpb.FileOptions{
					GoPackage: googleproto.String("google.golang.org/protobuf/types/pluginpb"),
				},
			},
			{
				Name:    googleproto.String("google/api/annotations.proto"),
				Package: googleproto.String("google.api"),
				Options: &descriptorpb.FileOptions{
					GoPackage: googleproto.String("google.golang.org/genproto/googleapis/api/annotations;annotations"),
				},
			},
			{
				Name:    googleproto.String("buf/validate/validate.proto"),
				Package: googleproto.String("buf.validate"),
				Options: &descriptorpb.FileOptions{
					GoPackage: googleproto.String("buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"),
				},
			},
			{
				Name:       googleproto.String("saas/accounts/v1/accounts.proto"),
				Package:    googleproto.String("saas.accounts.v1"),
				Dependency: []string{"google/protobuf/timestamp.proto", "google/api/annotations.proto"},
			},
			{
				// Vendoring a copy of the well-known types puts real, module-owned
				// files at those paths; the proto package is what tells them apart.
				Name:    googleproto.String("google/protobuf/accounts_extras.proto"),
				Package: googleproto.String("saas.accounts.v1"),
			},
		},
	}
}

func marshal(t *testing.T, set *descriptorpb.FileDescriptorSet) []byte {
	t.Helper()
	data, err := googleproto.Marshal(set)
	require.NoError(t, err)
	return data
}

// TestMarkForeignImports pins which files of a descriptor set buf is told not to
// generate for: every namespace whose bindings someone else publishes. Vendoring
// any of them into the generated library puts a second registration of the same
// proto file name into the consumer's descriptor pool, which panics at init as
// soon as the consumer also links the canonical package.
//
// Marking the module's own files would generate nothing at all, so a file living
// under google/protobuf/ but declaring the module's own package is the module's,
// not a well-known type.
func TestMarkForeignImports(t *testing.T) {
	data, _, err := MarkForeignImports(marshal(t, foreignSet()), languages.GO)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(data, &image))

	want := map[string]bool{
		"google/protobuf/timestamp.proto":       true,
		"google/protobuf/compiler/plugin.proto": true,
		"google/api/annotations.proto":          true,
		"buf/validate/validate.proto":           true,
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
		image.GetFile()[4].GetDependency())
}

// TestMarkForeignImportsTypeScriptKeepsRelativelyImportedNamespaces pins the
// half of the answer that is language-specific. Dropping a namespace only works
// if the generated bindings name it: protoc-gen-es publishes the well-known
// types as @bufbuild/protobuf/wkt and imports them from there, but it has no
// package for googleapis or protovalidate and emits a path relative to the file
// it generates. Mark those two and the library that results imports
// ../../google/api/annotations_pb and ../../buf/validate/validate_pb from files
// buf was told not to write — tsc fails with TS2307 on every one of them.
func TestMarkForeignImportsTypeScriptKeepsRelativelyImportedNamespaces(t *testing.T) {
	data, _, err := MarkForeignImports(marshal(t, foreignSet()), languages.TYPESCRIPT)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(data, &image))

	want := map[string]bool{
		"google/protobuf/timestamp.proto":       true,
		"google/protobuf/compiler/plugin.proto": true,
		"google/api/annotations.proto":          false,
		"buf/validate/validate.proto":           false,
		"saas/accounts/v1/accounts.proto":       false,
		"google/protobuf/accounts_extras.proto": false,
	}
	require.Len(t, image.GetFile(), len(want))
	for _, file := range image.GetFile() {
		marked, known := want[file.GetName()]
		require.True(t, known, "unexpected file %s", file.GetName())
		require.Equal(t, marked, isMarkedAsImport(file), "file %s", file.GetName())
	}
}

// TestMarkForeignImportsReturnsGoPackageOverrides covers the half of the fix that
// marking alone does not achieve. buf's managed mode rewrites go_package for
// every file in the image, imports included, and its `except` list matches by buf
// module identity, which a plain FileDescriptorSet does not carry — so
// `except: buf.build/googleapis/googleapis` is inert here. Without these
// overrides fed back into buf.gen.yaml, google/api is rewritten to the generated
// library's own path and the module's bindings import a package nothing
// generated.
func TestMarkForeignImportsReturnsGoPackageOverrides(t *testing.T) {
	_, overrides, err := MarkForeignImports(marshal(t, foreignSet()), languages.GO)
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"google/protobuf/timestamp.proto":       "google.golang.org/protobuf/types/known/timestamppb",
		"google/protobuf/compiler/plugin.proto": "google.golang.org/protobuf/types/pluginpb",
		"google/api/annotations.proto":          "google.golang.org/genproto/googleapis/api/annotations;annotations",
		"buf/validate/validate.proto":           "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate",
	}, overrides)
}

// TestMarkForeignImportsIsIdempotent covers the composition that actually
// happens: a caller (the CLI's `generate client`) marks its own image before
// calling GenerateClient, which marks again. A second field-8042 submessage
// merges with the first and is_import=true merged onto true stays true, so no
// coordinated release between core and its callers is needed.
func TestMarkForeignImportsIsIdempotent(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{
			Name:    googleproto.String("google/protobuf/timestamp.proto"),
			Package: googleproto.String("google.protobuf"),
		}},
	}

	once, _, err := MarkForeignImports(marshal(t, set), languages.GO)
	require.NoError(t, err)
	twice, _, err := MarkForeignImports(once, languages.GO)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(twice, &image))
	require.Len(t, image.GetFile(), 1)
	require.True(t, isMarkedAsImport(image.GetFile()[0]))
}

// TestMarkForeignImportsWireFormat pins the marker's exact bytes. Every other
// assertion here is written against the same constants the marker is built from,
// so it would keep passing if those drifted from buf.alpha.image.v1's schema and
// buf silently resumed generating the foreign namespaces.
func TestMarkForeignImportsWireFormat(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{
			Name:    googleproto.String("google/protobuf/empty.proto"),
			Package: googleproto.String("google.protobuf"),
		}},
	}

	data, _, err := MarkForeignImports(marshal(t, set), languages.GO)
	require.NoError(t, err)

	var image descriptorpb.FileDescriptorSet
	require.NoError(t, googleproto.Unmarshal(data, &image))
	require.Equal(t,
		[]byte{0xd2, 0xf6, 0x03, 0x02, 0x08, 0x01},
		[]byte(image.GetFile()[0].ProtoReflect().GetUnknown()))
}

// MarkForeignImports rejects bytes that are not a FileDescriptorSet rather than
// passing them through: buf could not have generated from them either, and the
// caller learns which of its inputs is wrong.
func TestMarkForeignImportsRejectsGarbage(t *testing.T) {
	_, _, err := MarkForeignImports([]byte("not-a-descriptor-set"), languages.GO)
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

// TestRemoveForeignOutput covers the migration the marking creates. buf writes
// into the destination but nothing empties it, so a library generated before the
// foreign namespaces were carried as imports keeps its stale bindings: the Go
// build still fails with the very "found packages descriptorpb and durationpb"
// error the marking removes, and the fix looks like it did not work.
func TestRemoveForeignOutput(t *testing.T) {
	dest := t.TempDir()
	stale := []string{
		"google/protobuf/timestamp.pb.go",
		"google/protobuf/descriptor.pb.go",
		"google/protobuf/compiler/plugin.pb.go",
		"google/api/annotations.pb.go",
		"buf/validate/validate.pb.go",
	}
	for _, rel := range stale {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("package stale\n"), 0600))
	}
	// The module's own bindings are not the generator's to reclaim here: buf
	// overwrites them in the same run, and removing them would lose the output
	// of a run that later fails.
	own := filepath.Join(dest, "saas", "accounts", "v1", "accounts.pb.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(own), 0750))
	require.NoError(t, os.WriteFile(own, []byte("package accountsv1\n"), 0600))

	require.NoError(t, removeForeignOutput(context.Background(), dest, languages.GO))

	for _, rel := range stale {
		_, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel)))
		require.True(t, os.IsNotExist(err), "stale binding survived: %s", rel)
	}
	_, err := os.Stat(own)
	require.NoError(t, err, "the module's own bindings must survive")
}

// TestRemoveForeignOutputTypeScriptKeepsVendoredNamespaces is the clean half of
// the same invariant: what is cleaned has to be what is not emitted. A
// TypeScript library owns its google/api and buf/validate copies because its own
// bindings import them by relative path; removing them here would delete files
// this very run is about to write, and leave the tree without them whenever a
// run fails in between.
func TestRemoveForeignOutputTypeScriptKeepsVendoredNamespaces(t *testing.T) {
	dest := t.TempDir()
	files := map[string]bool{
		"google/protobuf/timestamp_pb.ts": false,
		"google/api/annotations_pb.ts":    true,
		"buf/validate/validate_pb.ts":     true,
		"saas/accounts/v1/accounts_pb.ts": true,
	}
	for rel := range files {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("export {};\n"), 0600))
	}

	require.NoError(t, removeForeignOutput(context.Background(), dest, languages.TYPESCRIPT))

	for rel, survives := range files {
		_, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel)))
		require.Equal(t, survives, err == nil, "%s", rel)
	}
}

// removeForeignOutput runs on every generation, including the very first one
// into an empty destination.
func TestRemoveForeignOutputOnEmptyDestination(t *testing.T) {
	require.NoError(t, removeForeignOutput(context.Background(), t.TempDir(), languages.GO))
}
