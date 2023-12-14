package shared

import "google.golang.org/protobuf/proto"

func ProtoType(m proto.Message) string {
	descriptor := m.ProtoReflect().Descriptor()
	return string(descriptor.Name())
}
