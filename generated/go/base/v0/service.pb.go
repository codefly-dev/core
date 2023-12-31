// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v0.32.0
// 	protoc        (unknown)
// source: base/v0/service.proto

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

// Service is the fundamental "live" computing unit of a system
// It belongs to an application
// It is "hosted" by an agent
// It has a set of endpoints
type Service struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string      `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description string      `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	Application string      `protobuf:"bytes,3,opt,name=application,proto3" json:"application,omitempty"`
	Agent       *Agent      `protobuf:"bytes,4,opt,name=agent,proto3" json:"agent,omitempty"`
	Endpoints   []*Endpoint `protobuf:"bytes,5,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
}

func (x *Service) Reset() {
	*x = Service{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Service) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Service) ProtoMessage() {}

func (x *Service) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_service_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Service.ProtoReflect.Descriptor instead.
func (*Service) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{0}
}

func (x *Service) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Service) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Service) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

func (x *Service) GetAgent() *Agent {
	if x != nil {
		return x.Agent
	}
	return nil
}

func (x *Service) GetEndpoints() []*Endpoint {
	if x != nil {
		return x.Endpoints
	}
	return nil
}

type Version struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Version string `protobuf:"bytes,1,opt,name=version,proto3" json:"version,omitempty"`
}

func (x *Version) Reset() {
	*x = Version{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Version) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Version) ProtoMessage() {}

func (x *Version) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_service_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Version.ProtoReflect.Descriptor instead.
func (*Version) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{1}
}

func (x *Version) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

// ServiceIdentity is the identity of a service
// It has several component depending on how we look at a software system:
// - Logical: corresponds to the Bounded Context of DDD
// - Resource: corresponds to a logical or computational group
// - Physical: corresponds to a physical group, where the code lives!
type ServiceIdentity struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Domain      string `protobuf:"bytes,2,opt,name=domain,proto3" json:"domain,omitempty"`
	Application string `protobuf:"bytes,3,opt,name=application,proto3" json:"application,omitempty"`
	Namespace   string `protobuf:"bytes,4,opt,name=namespace,proto3" json:"namespace,omitempty"`
	Location    string `protobuf:"bytes,5,opt,name=location,proto3" json:"location,omitempty"`
}

func (x *ServiceIdentity) Reset() {
	*x = ServiceIdentity{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ServiceIdentity) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ServiceIdentity) ProtoMessage() {}

func (x *ServiceIdentity) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_service_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ServiceIdentity.ProtoReflect.Descriptor instead.
func (*ServiceIdentity) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{2}
}

func (x *ServiceIdentity) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ServiceIdentity) GetDomain() string {
	if x != nil {
		return x.Domain
	}
	return ""
}

func (x *ServiceIdentity) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

func (x *ServiceIdentity) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *ServiceIdentity) GetLocation() string {
	if x != nil {
		return x.Location
	}
	return ""
}

var File_base_v0_service_proto protoreflect.FileDescriptor

var file_base_v0_service_proto_rawDesc = []byte{
	0x0a, 0x15, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x1a, 0x16, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69,
	0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x13, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xb8, 0x01,
	0x0a, 0x07, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d,
	0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a,
	0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12,
	0x20, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f,
	0x6e, 0x12, 0x24, 0x0a, 0x05, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x0e, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74,
	0x52, 0x05, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x12, 0x2f, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f,
	0x69, 0x6e, 0x74, 0x73, 0x18, 0x05, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x62, 0x61, 0x73,
	0x65, 0x2e, 0x76, 0x30, 0x2e, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x52, 0x09, 0x65,
	0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x22, 0x23, 0x0a, 0x07, 0x56, 0x65, 0x72, 0x73,
	0x69, 0x6f, 0x6e, 0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x22, 0x99, 0x01,
	0x0a, 0x0f, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x49, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x74,
	0x79, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x64, 0x6f, 0x6d, 0x61, 0x69, 0x6e, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x64, 0x6f, 0x6d, 0x61, 0x69, 0x6e, 0x12, 0x20, 0x0a,
	0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x03, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12,
	0x1c, 0x0a, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x1a, 0x0a,
	0x08, 0x6c, 0x6f, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x08, 0x6c, 0x6f, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x42, 0x8a, 0x01, 0x0a, 0x0b, 0x63, 0x6f,
	0x6d, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x0c, 0x53, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x30, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65,
	0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64,
	0x2f, 0x67, 0x6f, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x42, 0x56,
	0x58, 0xaa, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x07, 0x42, 0x61,
	0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x13, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c,
	0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x08, 0x42, 0x61,
	0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_base_v0_service_proto_rawDescOnce sync.Once
	file_base_v0_service_proto_rawDescData = file_base_v0_service_proto_rawDesc
)

func file_base_v0_service_proto_rawDescGZIP() []byte {
	file_base_v0_service_proto_rawDescOnce.Do(func() {
		file_base_v0_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_base_v0_service_proto_rawDescData)
	})
	return file_base_v0_service_proto_rawDescData
}

var file_base_v0_service_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_base_v0_service_proto_goTypes = []interface{}{
	(*Service)(nil),         // 0: base.v0.Service
	(*Version)(nil),         // 1: base.v0.Version
	(*ServiceIdentity)(nil), // 2: base.v0.ServiceIdentity
	(*Agent)(nil),           // 3: base.v0.Agent
	(*Endpoint)(nil),        // 4: base.v0.Endpoint
}
var file_base_v0_service_proto_depIdxs = []int32{
	3, // 0: base.v0.Service.agent:type_name -> base.v0.Agent
	4, // 1: base.v0.Service.endpoints:type_name -> base.v0.Endpoint
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_base_v0_service_proto_init() }
func file_base_v0_service_proto_init() {
	if File_base_v0_service_proto != nil {
		return
	}
	file_base_v0_endpoint_proto_init()
	file_base_v0_agent_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_base_v0_service_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Service); i {
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
		file_base_v0_service_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Version); i {
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
		file_base_v0_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ServiceIdentity); i {
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
			RawDescriptor: file_base_v0_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_base_v0_service_proto_goTypes,
		DependencyIndexes: file_base_v0_service_proto_depIdxs,
		MessageInfos:      file_base_v0_service_proto_msgTypes,
	}.Build()
	File_base_v0_service_proto = out.File
	file_base_v0_service_proto_rawDesc = nil
	file_base_v0_service_proto_goTypes = nil
	file_base_v0_service_proto_depIdxs = nil
}
