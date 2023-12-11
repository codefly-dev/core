package shared

import "google.golang.org/protobuf/proto"

func Type(m proto.Message) string {
	descriptor := m.ProtoReflect().Descriptor()
	return string(descriptor.Name())
}
