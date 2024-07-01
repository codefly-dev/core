// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/base/v0/module.proto

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

type Module struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The name of the module
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// A description of the module
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	// Services that are provided by this module
	Services []*Service `protobuf:"bytes,4,rep,name=services,proto3" json:"services,omitempty"`
	// (Optional) The entry point for the module
	ServiceEntry string `protobuf:"bytes,5,opt,name=service_entry,json=serviceEntry,proto3" json:"service_entry,omitempty"`
}

func (x *Module) Reset() {
	*x = Module{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_module_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Module) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Module) ProtoMessage() {}

func (x *Module) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_module_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Module.ProtoReflect.Descriptor instead.
func (*Module) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_module_proto_rawDescGZIP(), []int{0}
}

func (x *Module) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Module) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Module) GetServices() []*Service {
	if x != nil {
		return x.Services
	}
	return nil
}

func (x *Module) GetServiceEntry() string {
	if x != nil {
		return x.ServiceEntry
	}
	return ""
}

type ManagedModule struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id     string  `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Module *Module `protobuf:"bytes,2,opt,name=module,proto3" json:"module,omitempty"`
}

func (x *ManagedModule) Reset() {
	*x = ManagedModule{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_module_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ManagedModule) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ManagedModule) ProtoMessage() {}

func (x *ManagedModule) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_module_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ManagedModule.ProtoReflect.Descriptor instead.
func (*ManagedModule) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_module_proto_rawDescGZIP(), []int{1}
}

func (x *ManagedModule) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *ManagedModule) GetModule() *Module {
	if x != nil {
		return x.Module
	}
	return nil
}

var File_codefly_base_v0_module_proto protoreflect.FileDescriptor

var file_codefly_base_v0_module_proto_rawDesc = []byte{
	0x0a, 0x1c, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0f,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x1a,
	0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2f, 0x76, 0x61,
	0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1d, 0x63, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x99, 0x01, 0x0a, 0x06,
	0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65,
	0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x34, 0x0a, 0x08,
	0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x18,
	0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x52, 0x08, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x73, 0x12, 0x23, 0x0a, 0x0d, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x65, 0x6e,
	0x74, 0x72, 0x79, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0c, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x22, 0x64, 0x0a, 0x0d, 0x4d, 0x61, 0x6e, 0x61, 0x67,
	0x65, 0x64, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x22, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x42, 0x12, 0xba, 0x48, 0x0f, 0x72, 0x0d, 0x32, 0x0b, 0x5e, 0x5b, 0x61,
	0x2d, 0x7a, 0x5d, 0x7b, 0x31, 0x30, 0x7d, 0x24, 0x52, 0x02, 0x69, 0x64, 0x12, 0x2f, 0x0a, 0x06,
	0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x17, 0x2e, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x4d,
	0x6f, 0x64, 0x75, 0x6c, 0x65, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x42, 0xba, 0x01,
	0x0a, 0x13, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61,
	0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x0b, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x50, 0x72, 0x6f,
	0x74, 0x6f, 0x50, 0x01, 0x5a, 0x38, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d,
	0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72,
	0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2, 0x02,
	0x03, 0x43, 0x42, 0x56, 0xaa, 0x02, 0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x42,
	0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1b, 0x43, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65,
	0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x11, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x3a, 0x3a, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_codefly_base_v0_module_proto_rawDescOnce sync.Once
	file_codefly_base_v0_module_proto_rawDescData = file_codefly_base_v0_module_proto_rawDesc
)

func file_codefly_base_v0_module_proto_rawDescGZIP() []byte {
	file_codefly_base_v0_module_proto_rawDescOnce.Do(func() {
		file_codefly_base_v0_module_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_base_v0_module_proto_rawDescData)
	})
	return file_codefly_base_v0_module_proto_rawDescData
}

var file_codefly_base_v0_module_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_codefly_base_v0_module_proto_goTypes = []any{
	(*Module)(nil),        // 0: codefly.base.v0.Module
	(*ManagedModule)(nil), // 1: codefly.base.v0.ManagedModule
	(*Service)(nil),       // 2: codefly.base.v0.Service
}
var file_codefly_base_v0_module_proto_depIdxs = []int32{
	2, // 0: codefly.base.v0.Module.services:type_name -> codefly.base.v0.Service
	0, // 1: codefly.base.v0.ManagedModule.module:type_name -> codefly.base.v0.Module
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_codefly_base_v0_module_proto_init() }
func file_codefly_base_v0_module_proto_init() {
	if File_codefly_base_v0_module_proto != nil {
		return
	}
	file_codefly_base_v0_service_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_codefly_base_v0_module_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*Module); i {
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
		file_codefly_base_v0_module_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*ManagedModule); i {
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
			RawDescriptor: file_codefly_base_v0_module_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_base_v0_module_proto_goTypes,
		DependencyIndexes: file_codefly_base_v0_module_proto_depIdxs,
		MessageInfos:      file_codefly_base_v0_module_proto_msgTypes,
	}.Build()
	File_codefly_base_v0_module_proto = out.File
	file_codefly_base_v0_module_proto_rawDesc = nil
	file_codefly_base_v0_module_proto_goTypes = nil
	file_codefly_base_v0_module_proto_depIdxs = nil
}
