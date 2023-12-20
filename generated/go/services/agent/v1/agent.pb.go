// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: services/agent/v1/agent.proto

package agentv1

import (
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

type Language_Type int32

const (
	Language_GO         Language_Type = 0
	Language_PYTHON     Language_Type = 1
	Language_JAVASCRIPT Language_Type = 2
	Language_TYPESCRIPT Language_Type = 3
)

// Enum value maps for Language_Type.
var (
	Language_Type_name = map[int32]string{
		0: "GO",
		1: "PYTHON",
		2: "JAVASCRIPT",
		3: "TYPESCRIPT",
	}
	Language_Type_value = map[string]int32{
		"GO":         0,
		"PYTHON":     1,
		"JAVASCRIPT": 2,
		"TYPESCRIPT": 3,
	}
)

func (x Language_Type) Enum() *Language_Type {
	p := new(Language_Type)
	*p = x
	return p
}

func (x Language_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Language_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_services_agent_v1_agent_proto_enumTypes[0].Descriptor()
}

func (Language_Type) Type() protoreflect.EnumType {
	return &file_services_agent_v1_agent_proto_enumTypes[0]
}

func (x Language_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Language_Type.Descriptor instead.
func (Language_Type) EnumDescriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{0, 0}
}

type Protocol_Type int32

const (
	Protocol_HTTP Protocol_Type = 0
	Protocol_GRPC Protocol_Type = 1
)

// Enum value maps for Protocol_Type.
var (
	Protocol_Type_name = map[int32]string{
		0: "HTTP",
		1: "GRPC",
	}
	Protocol_Type_value = map[string]int32{
		"HTTP": 0,
		"GRPC": 1,
	}
)

func (x Protocol_Type) Enum() *Protocol_Type {
	p := new(Protocol_Type)
	*p = x
	return p
}

func (x Protocol_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Protocol_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_services_agent_v1_agent_proto_enumTypes[1].Descriptor()
}

func (Protocol_Type) Type() protoreflect.EnumType {
	return &file_services_agent_v1_agent_proto_enumTypes[1]
}

func (x Protocol_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Protocol_Type.Descriptor instead.
func (Protocol_Type) EnumDescriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{1, 0}
}

type Capability_Type int32

const (
	Capability_UNKNOWN Capability_Type = 0
	Capability_FACTORY Capability_Type = 1
	Capability_RUNTIME Capability_Type = 2
)

// Enum value maps for Capability_Type.
var (
	Capability_Type_name = map[int32]string{
		0: "UNKNOWN",
		1: "FACTORY",
		2: "RUNTIME",
	}
	Capability_Type_value = map[string]int32{
		"UNKNOWN": 0,
		"FACTORY": 1,
		"RUNTIME": 2,
	}
)

func (x Capability_Type) Enum() *Capability_Type {
	p := new(Capability_Type)
	*p = x
	return p
}

func (x Capability_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Capability_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_services_agent_v1_agent_proto_enumTypes[2].Descriptor()
}

func (Capability_Type) Type() protoreflect.EnumType {
	return &file_services_agent_v1_agent_proto_enumTypes[2]
}

func (x Capability_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Capability_Type.Descriptor instead.
func (Capability_Type) EnumDescriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{2, 0}
}

type Language struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type Language_Type `protobuf:"varint,1,opt,name=type,proto3,enum=agent.v1.Language_Type" json:"type,omitempty"`
}

func (x *Language) Reset() {
	*x = Language{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_agent_v1_agent_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Language) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Language) ProtoMessage() {}

func (x *Language) ProtoReflect() protoreflect.Message {
	mi := &file_services_agent_v1_agent_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Language.ProtoReflect.Descriptor instead.
func (*Language) Descriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{0}
}

func (x *Language) GetType() Language_Type {
	if x != nil {
		return x.Type
	}
	return Language_GO
}

type Protocol struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type Protocol_Type `protobuf:"varint,1,opt,name=type,proto3,enum=agent.v1.Protocol_Type" json:"type,omitempty"`
}

func (x *Protocol) Reset() {
	*x = Protocol{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_agent_v1_agent_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Protocol) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Protocol) ProtoMessage() {}

func (x *Protocol) ProtoReflect() protoreflect.Message {
	mi := &file_services_agent_v1_agent_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Protocol.ProtoReflect.Descriptor instead.
func (*Protocol) Descriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{1}
}

func (x *Protocol) GetType() Protocol_Type {
	if x != nil {
		return x.Type
	}
	return Protocol_HTTP
}

type Capability struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type Capability_Type `protobuf:"varint,1,opt,name=type,proto3,enum=agent.v1.Capability_Type" json:"type,omitempty"`
}

func (x *Capability) Reset() {
	*x = Capability{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_agent_v1_agent_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Capability) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Capability) ProtoMessage() {}

func (x *Capability) ProtoReflect() protoreflect.Message {
	mi := &file_services_agent_v1_agent_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Capability.ProtoReflect.Descriptor instead.
func (*Capability) Descriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{2}
}

func (x *Capability) GetType() Capability_Type {
	if x != nil {
		return x.Type
	}
	return Capability_UNKNOWN
}

type AgentInformation struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Capabilities []*Capability `protobuf:"bytes,1,rep,name=capabilities,proto3" json:"capabilities,omitempty"`
	Languages    []*Language   `protobuf:"bytes,2,rep,name=languages,proto3" json:"languages,omitempty"`
	Protocols    []*Protocol   `protobuf:"bytes,3,rep,name=protocols,proto3" json:"protocols,omitempty"`
	ReadMe       string        `protobuf:"bytes,4,opt,name=read_me,json=readMe,proto3" json:"read_me,omitempty"`
}

func (x *AgentInformation) Reset() {
	*x = AgentInformation{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_agent_v1_agent_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AgentInformation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AgentInformation) ProtoMessage() {}

func (x *AgentInformation) ProtoReflect() protoreflect.Message {
	mi := &file_services_agent_v1_agent_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AgentInformation.ProtoReflect.Descriptor instead.
func (*AgentInformation) Descriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{3}
}

func (x *AgentInformation) GetCapabilities() []*Capability {
	if x != nil {
		return x.Capabilities
	}
	return nil
}

func (x *AgentInformation) GetLanguages() []*Language {
	if x != nil {
		return x.Languages
	}
	return nil
}

func (x *AgentInformation) GetProtocols() []*Protocol {
	if x != nil {
		return x.Protocols
	}
	return nil
}

func (x *AgentInformation) GetReadMe() string {
	if x != nil {
		return x.ReadMe
	}
	return ""
}

type AgentInformationRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *AgentInformationRequest) Reset() {
	*x = AgentInformationRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_agent_v1_agent_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AgentInformationRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AgentInformationRequest) ProtoMessage() {}

func (x *AgentInformationRequest) ProtoReflect() protoreflect.Message {
	mi := &file_services_agent_v1_agent_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AgentInformationRequest.ProtoReflect.Descriptor instead.
func (*AgentInformationRequest) Descriptor() ([]byte, []int) {
	return file_services_agent_v1_agent_proto_rawDescGZIP(), []int{4}
}

var File_services_agent_v1_agent_proto protoreflect.FileDescriptor

var file_services_agent_v1_agent_proto_rawDesc = []byte{
	0x0a, 0x1d, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74,
	0x2f, 0x76, 0x31, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x08, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x22, 0x73, 0x0a, 0x08, 0x4c, 0x61, 0x6e,
	0x67, 0x75, 0x61, 0x67, 0x65, 0x12, 0x2b, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x0e, 0x32, 0x17, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x4c,
	0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79,
	0x70, 0x65, 0x22, 0x3a, 0x0a, 0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x06, 0x0a, 0x02, 0x47, 0x4f,
	0x10, 0x00, 0x12, 0x0a, 0x0a, 0x06, 0x50, 0x59, 0x54, 0x48, 0x4f, 0x4e, 0x10, 0x01, 0x12, 0x0e,
	0x0a, 0x0a, 0x4a, 0x41, 0x56, 0x41, 0x53, 0x43, 0x52, 0x49, 0x50, 0x54, 0x10, 0x02, 0x12, 0x0e,
	0x0a, 0x0a, 0x54, 0x59, 0x50, 0x45, 0x53, 0x43, 0x52, 0x49, 0x50, 0x54, 0x10, 0x03, 0x22, 0x53,
	0x0a, 0x08, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x12, 0x2b, 0x0a, 0x04, 0x74, 0x79,
	0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x17, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74,
	0x2e, 0x76, 0x31, 0x2e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x2e, 0x54, 0x79, 0x70,
	0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x1a, 0x0a, 0x04, 0x54, 0x79, 0x70, 0x65, 0x12,
	0x08, 0x0a, 0x04, 0x48, 0x54, 0x54, 0x50, 0x10, 0x00, 0x12, 0x08, 0x0a, 0x04, 0x47, 0x52, 0x50,
	0x43, 0x10, 0x01, 0x22, 0x6a, 0x0a, 0x0a, 0x43, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74,
	0x79, 0x12, 0x2d, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32,
	0x19, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x43, 0x61, 0x70, 0x61, 0x62,
	0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65,
	0x22, 0x2d, 0x0a, 0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x0b, 0x0a, 0x07, 0x55, 0x4e, 0x4b, 0x4e,
	0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12, 0x0b, 0x0a, 0x07, 0x46, 0x41, 0x43, 0x54, 0x4f, 0x52, 0x59,
	0x10, 0x01, 0x12, 0x0b, 0x0a, 0x07, 0x52, 0x55, 0x4e, 0x54, 0x49, 0x4d, 0x45, 0x10, 0x02, 0x22,
	0xc9, 0x01, 0x0a, 0x10, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x12, 0x38, 0x0a, 0x0c, 0x63, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69,
	0x74, 0x69, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x61, 0x67, 0x65,
	0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x43, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79,
	0x52, 0x0c, 0x63, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x69, 0x65, 0x73, 0x12, 0x30,
	0x0a, 0x09, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x12, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x4c, 0x61, 0x6e,
	0x67, 0x75, 0x61, 0x67, 0x65, 0x52, 0x09, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x73,
	0x12, 0x30, 0x0a, 0x09, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x73, 0x18, 0x03, 0x20,
	0x03, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x52, 0x09, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f,
	0x6c, 0x73, 0x12, 0x17, 0x0a, 0x07, 0x72, 0x65, 0x61, 0x64, 0x5f, 0x6d, 0x65, 0x18, 0x04, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x06, 0x72, 0x65, 0x61, 0x64, 0x4d, 0x65, 0x22, 0x19, 0x0a, 0x17, 0x41,
	0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x32, 0x5f, 0x0a, 0x05, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x12,
	0x56, 0x0a, 0x13, 0x47, 0x65, 0x74, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72,
	0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x21, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76,
	0x31, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69,
	0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1a, 0x2e, 0x61, 0x67, 0x65, 0x6e,
	0x74, 0x2e, 0x76, 0x31, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x00, 0x42, 0x9f, 0x01, 0x0a, 0x0c, 0x63, 0x6f, 0x6d, 0x2e,
	0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x42, 0x0a, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x42, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63,
	0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f,
	0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2f,
	0x76, 0x31, 0x3b, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x41, 0x58, 0x58,
	0xaa, 0x02, 0x08, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x08, 0x41, 0x67,
	0x65, 0x6e, 0x74, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x14, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x5c, 0x56,
	0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x09,
	0x41, 0x67, 0x65, 0x6e, 0x74, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_services_agent_v1_agent_proto_rawDescOnce sync.Once
	file_services_agent_v1_agent_proto_rawDescData = file_services_agent_v1_agent_proto_rawDesc
)

func file_services_agent_v1_agent_proto_rawDescGZIP() []byte {
	file_services_agent_v1_agent_proto_rawDescOnce.Do(func() {
		file_services_agent_v1_agent_proto_rawDescData = protoimpl.X.CompressGZIP(file_services_agent_v1_agent_proto_rawDescData)
	})
	return file_services_agent_v1_agent_proto_rawDescData
}

var file_services_agent_v1_agent_proto_enumTypes = make([]protoimpl.EnumInfo, 3)
var file_services_agent_v1_agent_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_services_agent_v1_agent_proto_goTypes = []interface{}{
	(Language_Type)(0),              // 0: agent.v1.Language.Type
	(Protocol_Type)(0),              // 1: agent.v1.Protocol.Type
	(Capability_Type)(0),            // 2: agent.v1.Capability.Type
	(*Language)(nil),                // 3: agent.v1.Language
	(*Protocol)(nil),                // 4: agent.v1.Protocol
	(*Capability)(nil),              // 5: agent.v1.Capability
	(*AgentInformation)(nil),        // 6: agent.v1.AgentInformation
	(*AgentInformationRequest)(nil), // 7: agent.v1.AgentInformationRequest
}
var file_services_agent_v1_agent_proto_depIdxs = []int32{
	0, // 0: agent.v1.Language.type:type_name -> agent.v1.Language.Type
	1, // 1: agent.v1.Protocol.type:type_name -> agent.v1.Protocol.Type
	2, // 2: agent.v1.Capability.type:type_name -> agent.v1.Capability.Type
	5, // 3: agent.v1.AgentInformation.capabilities:type_name -> agent.v1.Capability
	3, // 4: agent.v1.AgentInformation.languages:type_name -> agent.v1.Language
	4, // 5: agent.v1.AgentInformation.protocols:type_name -> agent.v1.Protocol
	7, // 6: agent.v1.Agent.GetAgentInformation:input_type -> agent.v1.AgentInformationRequest
	6, // 7: agent.v1.Agent.GetAgentInformation:output_type -> agent.v1.AgentInformation
	7, // [7:8] is the sub-list for method output_type
	6, // [6:7] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_services_agent_v1_agent_proto_init() }
func file_services_agent_v1_agent_proto_init() {
	if File_services_agent_v1_agent_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_services_agent_v1_agent_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Language); i {
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
		file_services_agent_v1_agent_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Protocol); i {
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
		file_services_agent_v1_agent_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Capability); i {
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
		file_services_agent_v1_agent_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*AgentInformation); i {
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
		file_services_agent_v1_agent_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*AgentInformationRequest); i {
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
			RawDescriptor: file_services_agent_v1_agent_proto_rawDesc,
			NumEnums:      3,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_services_agent_v1_agent_proto_goTypes,
		DependencyIndexes: file_services_agent_v1_agent_proto_depIdxs,
		EnumInfos:         file_services_agent_v1_agent_proto_enumTypes,
		MessageInfos:      file_services_agent_v1_agent_proto_msgTypes,
	}.Build()
	File_services_agent_v1_agent_proto = out.File
	file_services_agent_v1_agent_proto_rawDesc = nil
	file_services_agent_v1_agent_proto_goTypes = nil
	file_services_agent_v1_agent_proto_depIdxs = nil
}
