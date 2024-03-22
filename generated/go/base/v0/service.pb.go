// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.33.0
// 	protoc        (unknown)
// source: base/v0/service.proto

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

type ServiceReference struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Application string `protobuf:"bytes,2,opt,name=application,proto3" json:"application,omitempty"`
}

func (x *ServiceReference) Reset() {
	*x = ServiceReference{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ServiceReference) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ServiceReference) ProtoMessage() {}

func (x *ServiceReference) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use ServiceReference.ProtoReflect.Descriptor instead.
func (*ServiceReference) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{0}
}

func (x *ServiceReference) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ServiceReference) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

// Service is the fundamental "live" computing unit of a system
// It belongs to an application
// It is "hosted" by an agent
// It has a set of endpoints
type Service struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the service
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Short description of the the service
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	// Application to which the service belongs
	Application string `protobuf:"bytes,3,opt,name=application,proto3" json:"application,omitempty"`
	// Project to which the service belongs
	Project string `protobuf:"bytes,4,opt,name=project,proto3" json:"project,omitempty"`
	// Agent that represents the service
	Agent *Agent `protobuf:"bytes,5,opt,name=agent,proto3" json:"agent,omitempty"`
	// Endpoints exposed by the service
	Endpoints []*Endpoint `protobuf:"bytes,6,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
	// Dependencies
	ServiceDependencies []*ServiceReference `protobuf:"bytes,7,rep,name=service_dependencies,json=serviceDependencies,proto3" json:"service_dependencies,omitempty"`
}

func (x *Service) Reset() {
	*x = Service{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Service) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Service) ProtoMessage() {}

func (x *Service) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use Service.ProtoReflect.Descriptor instead.
func (*Service) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{1}
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

func (x *Service) GetProject() string {
	if x != nil {
		return x.Project
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

func (x *Service) GetServiceDependencies() []*ServiceReference {
	if x != nil {
		return x.ServiceDependencies
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
		mi := &file_base_v0_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Version) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Version) ProtoMessage() {}

func (x *Version) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use Version.ProtoReflect.Descriptor instead.
func (*Version) Descriptor() ([]byte, []int) {
	return file_base_v0_service_proto_rawDescGZIP(), []int{2}
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
	Application string `protobuf:"bytes,2,opt,name=application,proto3" json:"application,omitempty"`
	Project     string `protobuf:"bytes,3,opt,name=project,proto3" json:"project,omitempty"`
	Location    string `protobuf:"bytes,4,opt,name=location,proto3" json:"location,omitempty"`
}

func (x *ServiceIdentity) Reset() {
	*x = ServiceIdentity{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_service_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ServiceIdentity) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ServiceIdentity) ProtoMessage() {}

func (x *ServiceIdentity) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_service_proto_msgTypes[3]
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
	return file_base_v0_service_proto_rawDescGZIP(), []int{3}
}

func (x *ServiceIdentity) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ServiceIdentity) GetApplication() string {
	if x != nil {
		return x.Application
	}
	return ""
}

func (x *ServiceIdentity) GetProject() string {
	if x != nil {
		return x.Project
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
	0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2f, 0x76,
	0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x16, 0x62,
	0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x13, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x61,
	0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x5e, 0x0a, 0x10, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x12, 0x1d,
	0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48,
	0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x2b, 0x0a,
	0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x0b, 0x61,
	0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0xc1, 0x02, 0x0a, 0x07, 0x53,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x1d, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70,
	0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63,
	0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x2b, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48,
	0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x12, 0x23, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18,
	0x04, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32,
	0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x24, 0x0a, 0x05, 0x61, 0x67, 0x65,
	0x6e, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e,
	0x76, 0x30, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x52, 0x05, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x12,
	0x2f, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x18, 0x06, 0x20, 0x03,
	0x28, 0x0b, 0x32, 0x11, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x45, 0x6e, 0x64,
	0x70, 0x6f, 0x69, 0x6e, 0x74, 0x52, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73,
	0x12, 0x4c, 0x0a, 0x14, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x64, 0x65, 0x70, 0x65,
	0x6e, 0x64, 0x65, 0x6e, 0x63, 0x69, 0x65, 0x73, 0x18, 0x07, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x19,
	0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x52, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x52, 0x13, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x44, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x69, 0x65, 0x73, 0x22, 0x23,
	0x0a, 0x07, 0x56, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72,
	0x73, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73,
	0x69, 0x6f, 0x6e, 0x22, 0xa9, 0x01, 0x0a, 0x0f, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x49,
	0x64, 0x65, 0x6e, 0x74, 0x69, 0x74, 0x79, 0x12, 0x1d, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32,
	0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x2b, 0x0a, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06,
	0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x12, 0x23, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72, 0x04, 0x10, 0x03, 0x18, 0x32, 0x52,
	0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x25, 0x0a, 0x08, 0x6c, 0x6f, 0x63, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x42, 0x09, 0xba, 0x48, 0x06, 0x72,
	0x04, 0x10, 0x03, 0x18, 0x32, 0x52, 0x08, 0x6c, 0x6f, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x42,
	0x8a, 0x01, 0x0a, 0x0b, 0x63, 0x6f, 0x6d, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x42,
	0x0c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a,
	0x30, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e,
	0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0xa2, 0x02, 0x03, 0x42, 0x56, 0x58, 0xaa, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56,
	0x30, 0xca, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x13, 0x42, 0x61,
	0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0xea, 0x02, 0x08, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x33,
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

var file_base_v0_service_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_base_v0_service_proto_goTypes = []interface{}{
	(*ServiceReference)(nil), // 0: base.v0.ServiceReference
	(*Service)(nil),          // 1: base.v0.Service
	(*Version)(nil),          // 2: base.v0.Version
	(*ServiceIdentity)(nil),  // 3: base.v0.ServiceIdentity
	(*Agent)(nil),            // 4: base.v0.Agent
	(*Endpoint)(nil),         // 5: base.v0.Endpoint
}
var file_base_v0_service_proto_depIdxs = []int32{
	4, // 0: base.v0.Service.agent:type_name -> base.v0.Agent
	5, // 1: base.v0.Service.endpoints:type_name -> base.v0.Endpoint
	0, // 2: base.v0.Service.service_dependencies:type_name -> base.v0.ServiceReference
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
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
			switch v := v.(*ServiceReference); i {
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
		file_base_v0_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
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
		file_base_v0_service_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
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
			NumMessages:   4,
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
