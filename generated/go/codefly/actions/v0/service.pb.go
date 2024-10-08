// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/actions/v0/service.proto

package v0

import (
	_ "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	v0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type AddService struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind string `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	// name is the name of the service.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// description provides a brief explanation of the service.
	Description string `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	// agent is the agent that the service belongs to.
	Agent *v0.Agent `protobuf:"bytes,4,opt,name=agent,proto3" json:"agent,omitempty"`
}

func (x *AddService) Reset() {
	*x = AddService{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_actions_v0_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddService) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddService) ProtoMessage() {}

func (x *AddService) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_actions_v0_service_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AddService.ProtoReflect.Descriptor instead.
func (*AddService) Descriptor() ([]byte, []int) {
	return file_codefly_actions_v0_service_proto_rawDescGZIP(), []int{0}
}

func (x *AddService) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *AddService) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *AddService) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *AddService) GetAgent() *v0.Agent {
	if x != nil {
		return x.Agent
	}
	return nil
}

type AddServiceDependency struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// kind is the type of the service.
	Kind string `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	// name is the name of the service.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// module is the name of the module that the service belongs to.
	Module string `protobuf:"bytes,4,opt,name=module,proto3" json:"module,omitempty"`
	// dependency_name is the name of the dependency.
	DependencyName string `protobuf:"bytes,5,opt,name=dependency_name,json=dependencyName,proto3" json:"dependency_name,omitempty"`
	// dependency_module is the name of the module that the dependency belongs to.
	DependencyModule string `protobuf:"bytes,6,opt,name=dependency_module,json=dependencyModule,proto3" json:"dependency_module,omitempty"`
	// endpoints are the endpoints that the service can connect to.
	Endpoints []string `protobuf:"bytes,7,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
}

func (x *AddServiceDependency) Reset() {
	*x = AddServiceDependency{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_actions_v0_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddServiceDependency) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddServiceDependency) ProtoMessage() {}

func (x *AddServiceDependency) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_actions_v0_service_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AddServiceDependency.ProtoReflect.Descriptor instead.
func (*AddServiceDependency) Descriptor() ([]byte, []int) {
	return file_codefly_actions_v0_service_proto_rawDescGZIP(), []int{1}
}

func (x *AddServiceDependency) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *AddServiceDependency) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *AddServiceDependency) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *AddServiceDependency) GetDependencyName() string {
	if x != nil {
		return x.DependencyName
	}
	return ""
}

func (x *AddServiceDependency) GetDependencyModule() string {
	if x != nil {
		return x.DependencyModule
	}
	return ""
}

func (x *AddServiceDependency) GetEndpoints() []string {
	if x != nil {
		return x.Endpoints
	}
	return nil
}

var File_codefly_actions_v0_service_proto protoreflect.FileDescriptor

var file_codefly_actions_v0_service_proto_rawDesc = []byte{
	0x0a, 0x20, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e,
	0x73, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x12, 0x12, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x61, 0x63, 0x74, 0x69,
	0x6f, 0x6e, 0x73, 0x2e, 0x76, 0x30, 0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69,
	0x64, 0x61, 0x74, 0x65, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x1a, 0x1b, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73,
	0x65, 0x2f, 0x76, 0x30, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x22, 0x8f, 0x01, 0x0a, 0x0a, 0x41, 0x64, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12,
	0x12, 0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b,
	0x69, 0x6e, 0x64, 0x12, 0x1d, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f,
	0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70,
	0x74, 0x69, 0x6f, 0x6e, 0x12, 0x2c, 0x0a, 0x05, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x18, 0x04, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61,
	0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x52, 0x05, 0x61, 0x67, 0x65,
	0x6e, 0x74, 0x22, 0xf6, 0x01, 0x0a, 0x14, 0x41, 0x64, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x44, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x12, 0x12, 0x0a, 0x04, 0x6b,
	0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x12,
	0x1d, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba,
	0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x21,
	0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09,
	0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c,
	0x65, 0x12, 0x32, 0x0a, 0x0f, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x5f,
	0x6e, 0x61, 0x6d, 0x65, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72,
	0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x0e, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63,
	0x79, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x36, 0x0a, 0x11, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65,
	0x6e, 0x63, 0x79, 0x5f, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09,
	0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x10, 0x64, 0x65, 0x70,
	0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x1c, 0x0a,
	0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x18, 0x07, 0x20, 0x03, 0x28, 0x09,
	0x52, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x42, 0xcd, 0x01, 0x0a, 0x16,
	0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x61, 0x63, 0x74, 0x69,
	0x6f, 0x6e, 0x73, 0x2e, 0x76, 0x30, 0x42, 0x0c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x3b, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63,
	0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f,
	0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73,
	0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x43, 0x41, 0x56, 0xaa, 0x02, 0x12, 0x43, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x56, 0x30, 0xca, 0x02,
	0x12, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73,
	0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1e, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x41, 0x63,
	0x74, 0x69, 0x6f, 0x6e, 0x73, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61,
	0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x14, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a,
	0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_codefly_actions_v0_service_proto_rawDescOnce sync.Once
	file_codefly_actions_v0_service_proto_rawDescData = file_codefly_actions_v0_service_proto_rawDesc
)

func file_codefly_actions_v0_service_proto_rawDescGZIP() []byte {
	file_codefly_actions_v0_service_proto_rawDescOnce.Do(func() {
		file_codefly_actions_v0_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_actions_v0_service_proto_rawDescData)
	})
	return file_codefly_actions_v0_service_proto_rawDescData
}

var file_codefly_actions_v0_service_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_codefly_actions_v0_service_proto_goTypes = []any{
	(*AddService)(nil),           // 0: codefly.actions.v0.AddService
	(*AddServiceDependency)(nil), // 1: codefly.actions.v0.AddServiceDependency
	(*v0.Agent)(nil),             // 2: codefly.base.v0.Agent
}
var file_codefly_actions_v0_service_proto_depIdxs = []int32{
	2, // 0: codefly.actions.v0.AddService.agent:type_name -> codefly.base.v0.Agent
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_codefly_actions_v0_service_proto_init() }
func file_codefly_actions_v0_service_proto_init() {
	if File_codefly_actions_v0_service_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_codefly_actions_v0_service_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*AddService); i {
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
		file_codefly_actions_v0_service_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*AddServiceDependency); i {
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
			RawDescriptor: file_codefly_actions_v0_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_actions_v0_service_proto_goTypes,
		DependencyIndexes: file_codefly_actions_v0_service_proto_depIdxs,
		MessageInfos:      file_codefly_actions_v0_service_proto_msgTypes,
	}.Build()
	File_codefly_actions_v0_service_proto = out.File
	file_codefly_actions_v0_service_proto_rawDesc = nil
	file_codefly_actions_v0_service_proto_goTypes = nil
	file_codefly_actions_v0_service_proto_depIdxs = nil
}
