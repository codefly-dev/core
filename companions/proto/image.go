package proto

import (
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// A protobuf well-known type is identified by both its file name and its proto
// package, never the path alone: a module that keeps its own .proto under a
// google/protobuf/ directory (vendoring a copy of the well-known types is the
// common reason) still declares its own package, and marking such a file as an
// import would silently drop the bindings the run exists to produce.
const (
	wellKnownTypePrefix  = "google/protobuf/"
	wellKnownTypePackage = "google.protobuf"
)

// buf marks a file that an image carries only to resolve imports with
// buf.alpha.image.v1.ImageFileExtension.is_import: an extension of
// FileDescriptorProto at field 8042, carrying is_import at field 1.
const (
	bufImageFileExtensionField = 8042
	bufImageFileIsImportField  = 1
)

// markWellKnownTypesAsImports marks every google/protobuf/*.proto file in the
// serialized FileDescriptorSet as a buf image import, and returns the
// re-serialized set.
//
// A descriptor set handed to GenerateClient is a *plain* FileDescriptorSet —
// a codefly contract is built with `buf build --as-file-descriptor-set`, which
// deliberately drops buf's image extensions — so buf treats every file it
// carries as a generation target and emits local bindings for the imports too.
// For Go that means one gen/google/protobuf directory holding a Go package per
// well-known type (descriptorpb, durationpb, ...), which the compiler rejects
// outright; the files are dead on top of that, since the generated bindings
// import the well-known types from google.golang.org/protobuf/types/known/*
// (buf's managed mode leaves their go_package alone).
//
// Only the well-known types are marked. The other shared imports (google/api,
// buf.validate) do get their go_package rewritten by managed mode, so the
// module's bindings reference them at the generated library's own path and
// their local copies are load-bearing — marking those leaves the library
// importing a package nothing generated.
//
// A caller that already marked its image loses nothing by this running again:
// a second field-8042 submessage merges with the first, and is_import=true
// merged onto true stays true.
func markWellKnownTypesAsImports(descriptorSet []byte) ([]byte, error) {
	var set descriptorpb.FileDescriptorSet
	if err := googleproto.Unmarshal(descriptorSet, &set); err != nil {
		return nil, err
	}
	isImport := protowire.AppendVarint(protowire.AppendTag(nil, bufImageFileIsImportField, protowire.VarintType), 1)
	extension := protowire.AppendBytes(protowire.AppendTag(nil, bufImageFileExtensionField, protowire.BytesType), isImport)
	for _, file := range set.GetFile() {
		if !strings.HasPrefix(file.GetName(), wellKnownTypePrefix) || file.GetPackage() != wellKnownTypePackage {
			continue
		}
		message := file.ProtoReflect()
		unknown := append(protoreflect.RawFields(nil), message.GetUnknown()...)
		message.SetUnknown(append(unknown, extension...))
	}
	return googleproto.Marshal(&set)
}
