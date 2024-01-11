// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.32.0
// 	protoc        (unknown)
// source: base/v0/application.proto

package v0

import (
	reflect "reflect"
	sync "sync"

	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Application struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string     `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description string     `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	Project     string     `protobuf:"bytes,3,opt,name=project,proto3" json:"project,omitempty"`
	Services    []*Service `protobuf:"bytes,4,rep,name=services,proto3" json:"services,omitempty"`
}

func (x *Application) Reset() {
	*x = Application{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_application_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Application) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Application) ProtoMessage() {}

func (x *Application) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_application_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Application.ProtoReflect.Descriptor instead.
func (*Application) Descriptor() ([]byte, []int) {
	return file_base_v0_application_proto_rawDescGZIP(), []int{0}
}

func (x *Application) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Application) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Application) GetProject() string {
	if x != nil {
		return x.Project
	}
	return ""
}

func (x *Application) GetServices() []*Service {
	if x != nil {
		return x.Services
	}
	return nil
}

var File_base_v0_application_proto protoreflect.FileDescriptor

var file_base_v0_application_proto_rawDesc = []byte{
	0x0a, 0x19, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x62, 0x61, 0x73,
	0x65, 0x2e, 0x76, 0x30, 0x1a, 0x15, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x8b, 0x01, 0x0a, 0x0b,
	0x41, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x12, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12,
	0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f,
	0x6e, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x03, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x2c, 0x0a, 0x08, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x10, 0x2e,
	0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x52,
	0x08, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x42, 0x8e, 0x01, 0x0a, 0x0b, 0x63, 0x6f,
	0x6d, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x10, 0x41, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x30, 0x67,
	0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72,
	0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2,
	0x02, 0x03, 0x42, 0x56, 0x58, 0xaa, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca,
	0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x13, 0x42, 0x61, 0x73, 0x65,
	0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea,
	0x02, 0x08, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_base_v0_application_proto_rawDescOnce sync.Once
	file_base_v0_application_proto_rawDescData = file_base_v0_application_proto_rawDesc
)

func file_base_v0_application_proto_rawDescGZIP() []byte {
	file_base_v0_application_proto_rawDescOnce.Do(func() {
		file_base_v0_application_proto_rawDescData = protoimpl.X.CompressGZIP(file_base_v0_application_proto_rawDescData)
	})
	return file_base_v0_application_proto_rawDescData
}

var file_base_v0_application_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_base_v0_application_proto_goTypes = []interface{}{
	(*Application)(nil), // 0: base.v0.Application
	(*Service)(nil),     // 1: base.v0.Service
}
var file_base_v0_application_proto_depIdxs = []int32{
	1, // 0: base.v0.Application.services:type_name -> base.v0.Service
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_base_v0_application_proto_init() }
func file_base_v0_application_proto_init() {
	if File_base_v0_application_proto != nil {
		return
	}
	file_base_v0_service_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_base_v0_application_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Application); i {
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
			RawDescriptor: file_base_v0_application_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_base_v0_application_proto_goTypes,
		DependencyIndexes: file_base_v0_application_proto_depIdxs,
		MessageInfos:      file_base_v0_application_proto_msgTypes,
	}.Build()
	File_base_v0_application_proto = out.File
	file_base_v0_application_proto_rawDesc = nil
	file_base_v0_application_proto_goTypes = nil
	file_base_v0_application_proto_depIdxs = nil
}
