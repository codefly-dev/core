// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.1
// 	protoc        (unknown)
// source: actions/v0/module.proto

package v0

import (
	reflect "reflect"
	sync "sync"

	_ "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type NewModule struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Kind is the kind of the message.
	Kind string `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	// name is the name of the module.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// description provides a brief explanation of the module.
	Description string `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
}

func (x *NewModule) Reset() {
	*x = NewModule{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actions_v0_module_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NewModule) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NewModule) ProtoMessage() {}

func (x *NewModule) ProtoReflect() protoreflect.Message {
	mi := &file_actions_v0_module_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NewModule.ProtoReflect.Descriptor instead.
func (*NewModule) Descriptor() ([]byte, []int) {
	return file_actions_v0_module_proto_rawDescGZIP(), []int{0}
}

func (x *NewModule) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *NewModule) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *NewModule) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

var File_actions_v0_module_proto protoreflect.FileDescriptor

var file_actions_v0_module_proto_rawDesc = []byte{
	0x0a, 0x17, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2f, 0x76, 0x30, 0x2f, 0x6d, 0x6f, 0x64,
	0x75, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x61, 0x63, 0x74, 0x69, 0x6f,
	0x6e, 0x73, 0x2e, 0x76, 0x30, 0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64,
	0x61, 0x74, 0x65, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x22, 0x60, 0x0a, 0x09, 0x4e, 0x65, 0x77, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12,
	0x12, 0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b,
	0x69, 0x6e, 0x64, 0x12, 0x1d, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f,
	0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70,
	0x74, 0x69, 0x6f, 0x6e, 0x42, 0x9b, 0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d, 0x2e, 0x61, 0x63, 0x74,
	0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x76, 0x30, 0x42, 0x0b, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x33, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63,
	0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f,
	0x2f, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x41, 0x56,
	0x58, 0xaa, 0x02, 0x0a, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x56, 0x30, 0xca, 0x02,
	0x0a, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x16, 0x41, 0x63,
	0x74, 0x69, 0x6f, 0x6e, 0x73, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61,
	0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0b, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x3a, 0x3a,
	0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_actions_v0_module_proto_rawDescOnce sync.Once
	file_actions_v0_module_proto_rawDescData = file_actions_v0_module_proto_rawDesc
)

func file_actions_v0_module_proto_rawDescGZIP() []byte {
	file_actions_v0_module_proto_rawDescOnce.Do(func() {
		file_actions_v0_module_proto_rawDescData = protoimpl.X.CompressGZIP(file_actions_v0_module_proto_rawDescData)
	})
	return file_actions_v0_module_proto_rawDescData
}

var file_actions_v0_module_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_actions_v0_module_proto_goTypes = []interface{}{
	(*NewModule)(nil), // 0: actions.v0.NewModule
}
var file_actions_v0_module_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_actions_v0_module_proto_init() }
func file_actions_v0_module_proto_init() {
	if File_actions_v0_module_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_actions_v0_module_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*NewModule); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_actions_v0_module_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_actions_v0_module_proto_goTypes,
		DependencyIndexes: file_actions_v0_module_proto_depIdxs,
		MessageInfos:      file_actions_v0_module_proto_msgTypes,
	}.Build()
	File_actions_v0_module_proto = out.File
	file_actions_v0_module_proto_rawDesc = nil
	file_actions_v0_module_proto_goTypes = nil
	file_actions_v0_module_proto_depIdxs = nil
}
