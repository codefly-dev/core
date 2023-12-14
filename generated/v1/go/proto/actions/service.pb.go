// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: proto/actions/service.proto

package actions

import (
	base "github.com/codefly-dev/core/generated/v1/go/proto/base"
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

	Kind        string      `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	Override    bool        `protobuf:"varint,2,opt,name=override,proto3" json:"override,omitempty"`
	Name        string      `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	Description string      `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
	Application string      `protobuf:"bytes,5,opt,name=application,proto3" json:"application,omitempty"`
	Project     string      `protobuf:"bytes,6,opt,name=project,proto3" json:"project,omitempty"`
	Namespace   string      `protobuf:"bytes,7,opt,name=namespace,proto3" json:"namespace,omitempty"`
	Agent       *base.Agent `protobuf:"bytes,8,opt,name=agent,proto3" json:"agent,omitempty"`
	Path        string      `protobuf:"bytes,9,opt,name=path,proto3" json:"path,omitempty"`
}

func (x *AddService) Reset() {
	*x = AddService{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_actions_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddService) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddService) ProtoMessage() {}

func (x *AddService) ProtoReflect() protoreflect.Message {
	mi := &file_proto_actions_service_proto_msgTypes[0]
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
	return file_proto_actions_service_proto_rawDescGZIP(), []int{0}
}

func (x *AddService) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *AddService) GetOverride() bool {
	if x != nil {
		return x.Override
	}
	return false
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

func (x *AddService) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

func (x *AddService) GetProject() string {
	if x != nil {
		return x.Project
	}
	return ""
}

func (x *AddService) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *AddService) GetAgent() *base.Agent {
	if x != nil {
		return x.Agent
	}
	return nil
}

func (x *AddService) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

type SetServiceActive struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind        string `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	Name        string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Application string `protobuf:"bytes,3,opt,name=application,proto3" json:"application,omitempty"`
}

func (x *SetServiceActive) Reset() {
	*x = SetServiceActive{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_actions_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SetServiceActive) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetServiceActive) ProtoMessage() {}

func (x *SetServiceActive) ProtoReflect() protoreflect.Message {
	mi := &file_proto_actions_service_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetServiceActive.ProtoReflect.Descriptor instead.
func (*SetServiceActive) Descriptor() ([]byte, []int) {
	return file_proto_actions_service_proto_rawDescGZIP(), []int{1}
}

func (x *SetServiceActive) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *SetServiceActive) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *SetServiceActive) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

type AddServiceDependency struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind                  string   `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	Name                  string   `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Application           string   `protobuf:"bytes,3,opt,name=application,proto3" json:"application,omitempty"`
	DependencyName        string   `protobuf:"bytes,4,opt,name=dependency_name,json=dependencyName,proto3" json:"dependency_name,omitempty"`
	DependencyApplication string   `protobuf:"bytes,5,opt,name=dependency_application,json=dependencyApplication,proto3" json:"dependency_application,omitempty"`
	Project               string   `protobuf:"bytes,6,opt,name=project,proto3" json:"project,omitempty"`
	Endpoints             []string `protobuf:"bytes,7,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
}

func (x *AddServiceDependency) Reset() {
	*x = AddServiceDependency{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_actions_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddServiceDependency) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddServiceDependency) ProtoMessage() {}

func (x *AddServiceDependency) ProtoReflect() protoreflect.Message {
	mi := &file_proto_actions_service_proto_msgTypes[2]
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
	return file_proto_actions_service_proto_rawDescGZIP(), []int{2}
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

func (x *AddServiceDependency) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

func (x *AddServiceDependency) GetDependencyName() string {
	if x != nil {
		return x.DependencyName
	}
	return ""
}

func (x *AddServiceDependency) GetDependencyApplication() string {
	if x != nil {
		return x.DependencyApplication
	}
	return ""
}

func (x *AddServiceDependency) GetProject() string {
	if x != nil {
		return x.Project
	}
	return ""
}

func (x *AddServiceDependency) GetEndpoints() []string {
	if x != nil {
		return x.Endpoints
	}
	return nil
}

var File_proto_actions_service_proto protoreflect.FileDescriptor

var file_proto_actions_service_proto_rawDesc = []byte{
	0x0a, 0x1b, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2f,
	0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x76,
	0x31, 0x2e, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x1a, 0x16, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x1a, 0x19, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x65, 0x6e,
	0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x86, 0x02, 0x0a,
	0x0a, 0x41, 0x64, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6b,
	0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x12,
	0x1a, 0x0a, 0x08, 0x6f, 0x76, 0x65, 0x72, 0x72, 0x69, 0x64, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x08, 0x52, 0x08, 0x6f, 0x76, 0x65, 0x72, 0x72, 0x69, 0x64, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12,
	0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f,
	0x6e, 0x12, 0x20, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x06,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x1c, 0x0a,
	0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x07, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x24, 0x0a, 0x05, 0x61,
	0x67, 0x65, 0x6e, 0x74, 0x18, 0x08, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x76, 0x31, 0x2e,
	0x62, 0x61, 0x73, 0x65, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x52, 0x05, 0x61, 0x67, 0x65, 0x6e,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x61, 0x74, 0x68, 0x18, 0x09, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x70, 0x61, 0x74, 0x68, 0x22, 0x5c, 0x0a, 0x10, 0x53, 0x65, 0x74, 0x53, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x41, 0x63, 0x74, 0x69, 0x76, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6b, 0x69, 0x6e,
	0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x12, 0x12, 0x0a,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d,
	0x65, 0x12, 0x20, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x22, 0xf8, 0x01, 0x0a, 0x14, 0x41, 0x64, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x44, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x12, 0x12, 0x0a, 0x04,
	0x6b, 0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b, 0x69, 0x6e, 0x64,
	0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x27, 0x0a, 0x0f, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64,
	0x65, 0x6e, 0x63, 0x79, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x0e, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x4e, 0x61, 0x6d, 0x65, 0x12,
	0x35, 0x0a, 0x16, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x5f, 0x61, 0x70,
	0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x15, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x79, 0x41, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63,
	0x74, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74,
	0x12, 0x1c, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x18, 0x07, 0x20,
	0x03, 0x28, 0x09, 0x52, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x42, 0xa2,
	0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d, 0x2e, 0x76, 0x31, 0x2e, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e,
	0x73, 0x42, 0x0c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50,
	0x01, 0x5a, 0x39, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67,
	0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x76, 0x31, 0x2f, 0x67, 0x6f, 0x2f, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0xa2, 0x02, 0x03, 0x56,
	0x41, 0x58, 0xaa, 0x02, 0x0a, 0x56, 0x31, 0x2e, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0xca,
	0x02, 0x0a, 0x56, 0x31, 0x5c, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0xe2, 0x02, 0x16, 0x56,
	0x31, 0x5c, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0b, 0x56, 0x31, 0x3a, 0x3a, 0x41, 0x63, 0x74, 0x69,
	0x6f, 0x6e, 0x73, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_proto_actions_service_proto_rawDescOnce sync.Once
	file_proto_actions_service_proto_rawDescData = file_proto_actions_service_proto_rawDesc
)

func file_proto_actions_service_proto_rawDescGZIP() []byte {
	file_proto_actions_service_proto_rawDescOnce.Do(func() {
		file_proto_actions_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_proto_actions_service_proto_rawDescData)
	})
	return file_proto_actions_service_proto_rawDescData
}

var file_proto_actions_service_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_proto_actions_service_proto_goTypes = []interface{}{
	(*AddService)(nil),           // 0: v1.actions.AddService
	(*SetServiceActive)(nil),     // 1: v1.actions.SetServiceActive
	(*AddServiceDependency)(nil), // 2: v1.actions.AddServiceDependency
	(*base.Agent)(nil),           // 3: v1.base.Agent
}
var file_proto_actions_service_proto_depIdxs = []int32{
	3, // 0: v1.actions.AddService.agent:type_name -> v1.base.Agent
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_proto_actions_service_proto_init() }
func file_proto_actions_service_proto_init() {
	if File_proto_actions_service_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_proto_actions_service_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
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
		file_proto_actions_service_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SetServiceActive); i {
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
		file_proto_actions_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
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
			RawDescriptor: file_proto_actions_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_proto_actions_service_proto_goTypes,
		DependencyIndexes: file_proto_actions_service_proto_depIdxs,
		MessageInfos:      file_proto_actions_service_proto_msgTypes,
	}.Build()
	File_proto_actions_service_proto = out.File
	file_proto_actions_service_proto_rawDesc = nil
	file_proto_actions_service_proto_goTypes = nil
	file_proto_actions_service_proto_depIdxs = nil
}
