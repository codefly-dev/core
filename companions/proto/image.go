package proto

import (
	"strings"

	"github.com/codefly-dev/core/languages"
	"google.golang.org/protobuf/encoding/protowire"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// A foreign namespace is a proto namespace whose bindings are published by
// someone else: the protobuf runtime ships google/protobuf/*, the
// googleapis-common-protos / genproto packages ship the rest of google/*, and
// protovalidate ships buf/validate/*. Generating a local copy of any of them
// puts a second registration of the same *proto file name* into the consumer's
// descriptor pool, which panics at init the moment the consumer also links the
// canonical package — the exact collision strip_custom_options.py exists to
// prevent on the Python side.
//
// A namespace is identified by both the file-name prefix and the proto package,
// never the path alone: a module that keeps its own .proto under a google/
// directory (vendoring a copy of the well-known types is the common reason)
// still declares its own package, and marking such a file as an import would
// silently drop the bindings the run exists to produce.
type foreignNamespace struct {
	pathPrefix    string
	packagePrefix string
}

// foreignNamespaces covers google/protobuf (package google.protobuf and
// google.protobuf.compiler), the rest of google/ (google.api, google.rpc,
// google.type, ...) and buf/validate. It deliberately mirrors the namespaces
// strip_custom_options.py refuses to vendor, so the Go path and the Python path
// agree on what the generated library is allowed to own.
var foreignNamespaces = []foreignNamespace{
	{pathPrefix: "google/", packagePrefix: "google."},
	{pathPrefix: "buf/validate/", packagePrefix: "buf.validate"},
}

// owns reports whether file belongs to this namespace.
func (ns foreignNamespace) owns(file *descriptorpb.FileDescriptorProto) bool {
	return strings.HasPrefix(file.GetName(), ns.pathPrefix) && strings.HasPrefix(file.GetPackage(), ns.packagePrefix)
}

// protobufESRuntimeFiles are the proto files protoc-gen-es imports from
// @bufbuild/protobuf/wkt instead of a path relative to the file it is
// generating. It is protobuf-es's own wktPublicImportPaths, enumerated rather
// than approximated by the google/protobuf/ prefix: the two are not the same
// set, and which files are in it changes with the generator (2.2.3 carried no
// cpp_features, go_features or java_features). Keep it in step with the
// protoc-gen-es the companion bakes — companion_plugins_test.go's
// protocGenEsRuntimeVersion.
//
// A foreign file outside this set — anything under google/api, buf/validate, or
// a google/protobuf file protobuf-es has no runtime export for — is emitted with
// a relative import, so a TypeScript library has to own it. Marking one as a buf
// image import does not move it upstream, it just leaves
// `../../google/api/annotations_pb` pointing at a file nothing wrote, and tsc
// fails with TS2307 on every reference.
var protobufESRuntimeFiles = map[string]bool{
	"google/protobuf/any.proto":             true,
	"google/protobuf/api.proto":             true,
	"google/protobuf/compiler/plugin.proto": true,
	"google/protobuf/cpp_features.proto":    true,
	"google/protobuf/descriptor.proto":      true,
	"google/protobuf/duration.proto":        true,
	"google/protobuf/empty.proto":           true,
	"google/protobuf/field_mask.proto":      true,
	"google/protobuf/go_features.proto":     true,
	"google/protobuf/java_features.proto":   true,
	"google/protobuf/source_context.proto":  true,
	"google/protobuf/struct.proto":          true,
	"google/protobuf/timestamp.proto":       true,
	"google/protobuf/type.proto":            true,
	"google/protobuf/wrappers.proto":        true,
}

// buf marks a file that an image carries only to resolve imports with
// buf.alpha.image.v1.ImageFileExtension.is_import: an extension of
// FileDescriptorProto at field 8042, carrying is_import at field 1.
const (
	bufImageFileExtensionField = 8042
	bufImageFileIsImportField  = 1
)

// isForeign reports whether a library generated for language must not own this
// file's bindings.
func isForeign(file *descriptorpb.FileDescriptorProto, language languages.Language) bool {
	owned := false
	for _, ns := range foreignNamespaces {
		if ns.owns(file) {
			owned = true
			break
		}
	}
	if !owned {
		return false
	}
	// Go and Python name a dropped file's package absolutely — a rewritten
	// go_package, an untouched `from buf.validate import validate_pb2` — so
	// every foreign namespace resolves to whatever the consumer installed.
	// TypeScript only does for the files protobuf-es publishes in its runtime.
	if language == languages.TYPESCRIPT {
		return protobufESRuntimeFiles[file.GetName()]
	}
	return true
}

// MarkForeignImports marks every file of a serialized FileDescriptorSet that
// belongs to a foreign namespace the generated library can drop as a buf image
// import, and returns the re-serialized set together with the go_package each
// marked file declares, keyed by file name.
//
// Which namespaces those are depends on language, because dropping one only
// works if the generator refers to it by package name: Go and Python do that for
// all of them, TypeScript only for the well-known types. See foreignNamespace.
//
// A descriptor set handed to GenerateClient is a *plain* FileDescriptorSet —
// a codefly contract is built with `buf build --as-file-descriptor-set`, which
// deliberately drops buf's image extensions — so buf treats every file it
// carries as a generation target and emits local bindings for the imports too.
//
// Marking is only half the fix. buf's managed mode rewrites go_package for
// every file in the image, imports included, and its `except` list matches by
// *module identity*, which a plain FileDescriptorSet does not carry — so
// `except: buf.build/googleapis/googleapis` is inert here and google/api would
// be rewritten to the generated library's own path, leaving the module's
// bindings importing a package nothing generated. The returned map is fed back
// into buf.gen.yaml as managed.override.GO_PACKAGE so each marked file keeps
// the go_package it declares, and the module's bindings reference the canonical
// upstream package (google.golang.org/genproto/..., prost-types, ...) exactly
// as they do on the Sources path.
//
// `buf generate --exclude-path google/protobuf` would drop the well-known types
// with no wire-format surgery at all, and was measured to do so. It is not used
// because it filters on the path alone: it cannot tell a vendored module-owned
// .proto under google/ from a real one, and it has no way to carry the
// go_package the override map needs.
//
// A file in a foreign namespace that declares no go_package cannot be given an
// override, so managed mode rewrites it and the generated Go fails to compile
// on a dangling import. That is deliberate: it is loud, and it beats the silent
// duplicate registration that vendoring the file produces. No such file exists
// in googleapis, protovalidate, or the well-known types.
//
// A caller that already marked its image loses nothing by this running again:
// a second field-8042 submessage merges with the first, and is_import=true
// merged onto true stays true.
func MarkForeignImports(descriptorSet []byte, language languages.Language) ([]byte, map[string]string, error) {
	var set descriptorpb.FileDescriptorSet
	if err := googleproto.Unmarshal(descriptorSet, &set); err != nil {
		return nil, nil, err
	}
	isImport := protowire.AppendVarint(protowire.AppendTag(nil, bufImageFileIsImportField, protowire.VarintType), 1)
	extension := protowire.AppendBytes(protowire.AppendTag(nil, bufImageFileExtensionField, protowire.BytesType), isImport)
	goPackages := make(map[string]string)
	for _, file := range set.GetFile() {
		if !isForeign(file, language) {
			continue
		}
		message := file.ProtoReflect()
		unknown := append(protoreflect.RawFields(nil), message.GetUnknown()...)
		message.SetUnknown(append(unknown, extension...))
		if pkg := file.GetOptions().GetGoPackage(); pkg != "" {
			goPackages[file.GetName()] = pkg
		}
	}
	marked, err := googleproto.Marshal(&set)
	if err != nil {
		return nil, nil, err
	}
	return marked, goPackages, nil
}
