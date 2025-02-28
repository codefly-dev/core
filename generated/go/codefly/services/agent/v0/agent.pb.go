// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        (unknown)
// source: codefly/services/agent/v0/agent.proto

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

type Language_Type int32

const (
	Language_GO         Language_Type = 0
	Language_PYTHON     Language_Type = 1
	Language_JAVASCRIPT Language_Type = 2
	Language_TYPESCRIPT Language_Type = 3
	Language_RUBY       Language_Type = 4
)

// Enum value maps for Language_Type.
var (
	Language_Type_name = map[int32]string{
		0: "GO",
		1: "PYTHON",
		2: "JAVASCRIPT",
		3: "TYPESCRIPT",
		4: "RUBY",
	}
	Language_Type_value = map[string]int32{
		"GO":         0,
		"PYTHON":     1,
		"JAVASCRIPT": 2,
		"TYPESCRIPT": 3,
		"RUBY":       4,
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
	return file_codefly_services_agent_v0_agent_proto_enumTypes[0].Descriptor()
}

func (Language_Type) Type() protoreflect.EnumType {
	return &file_codefly_services_agent_v0_agent_proto_enumTypes[0]
}

func (x Language_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Language_Type.Descriptor instead.
func (Language_Type) EnumDescriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{0, 0}
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
	return file_codefly_services_agent_v0_agent_proto_enumTypes[1].Descriptor()
}

func (Protocol_Type) Type() protoreflect.EnumType {
	return &file_codefly_services_agent_v0_agent_proto_enumTypes[1]
}

func (x Protocol_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Protocol_Type.Descriptor instead.
func (Protocol_Type) EnumDescriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{1, 0}
}

type Capability_Type int32

const (
	Capability_UNKNOWN Capability_Type = 0
	Capability_BUILDER Capability_Type = 1
	Capability_RUNTIME Capability_Type = 2
	// Hot Reload is a runtime where a running process will NOT be restarted
	Capability_HOT_RELOAD Capability_Type = 3
)

// Enum value maps for Capability_Type.
var (
	Capability_Type_name = map[int32]string{
		0: "UNKNOWN",
		1: "BUILDER",
		2: "RUNTIME",
		3: "HOT_RELOAD",
	}
	Capability_Type_value = map[string]int32{
		"UNKNOWN":    0,
		"BUILDER":    1,
		"RUNTIME":    2,
		"HOT_RELOAD": 3,
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
	return file_codefly_services_agent_v0_agent_proto_enumTypes[2].Descriptor()
}

func (Capability_Type) Type() protoreflect.EnumType {
	return &file_codefly_services_agent_v0_agent_proto_enumTypes[2]
}

func (x Capability_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Capability_Type.Descriptor instead.
func (Capability_Type) EnumDescriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{2, 0}
}

type Runtime_Type int32

const (
	Runtime_UNKNOWN       Runtime_Type = 0
	Runtime_GO            Runtime_Type = 1
	Runtime_NPM           Runtime_Type = 2
	Runtime_PYTHON        Runtime_Type = 3
	Runtime_PYTHON_POETRY Runtime_Type = 4
	Runtime_RUBY          Runtime_Type = 5
	Runtime_RUBY_GEM      Runtime_Type = 6
	Runtime_RUBY_BUNDLE   Runtime_Type = 7
)

// Enum value maps for Runtime_Type.
var (
	Runtime_Type_name = map[int32]string{
		0: "UNKNOWN",
		1: "GO",
		2: "NPM",
		3: "PYTHON",
		4: "PYTHON_POETRY",
		5: "RUBY",
		6: "RUBY_GEM",
		7: "RUBY_BUNDLE",
	}
	Runtime_Type_value = map[string]int32{
		"UNKNOWN":       0,
		"GO":            1,
		"NPM":           2,
		"PYTHON":        3,
		"PYTHON_POETRY": 4,
		"RUBY":          5,
		"RUBY_GEM":      6,
		"RUBY_BUNDLE":   7,
	}
)

func (x Runtime_Type) Enum() *Runtime_Type {
	p := new(Runtime_Type)
	*p = x
	return p
}

func (x Runtime_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Runtime_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_codefly_services_agent_v0_agent_proto_enumTypes[3].Descriptor()
}

func (Runtime_Type) Type() protoreflect.EnumType {
	return &file_codefly_services_agent_v0_agent_proto_enumTypes[3]
}

func (x Runtime_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Runtime_Type.Descriptor instead.
func (Runtime_Type) EnumDescriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{3, 0}
}

type Language struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type Language_Type `protobuf:"varint,1,opt,name=type,proto3,enum=codefly.services.agent.v0.Language_Type" json:"type,omitempty"`
}

func (x *Language) Reset() {
	*x = Language{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Language) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Language) ProtoMessage() {}

func (x *Language) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[0]
	if x != nil {
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
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{0}
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

	Type Protocol_Type `protobuf:"varint,1,opt,name=type,proto3,enum=codefly.services.agent.v0.Protocol_Type" json:"type,omitempty"`
}

func (x *Protocol) Reset() {
	*x = Protocol{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Protocol) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Protocol) ProtoMessage() {}

func (x *Protocol) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[1]
	if x != nil {
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
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{1}
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

	Type Capability_Type `protobuf:"varint,1,opt,name=type,proto3,enum=codefly.services.agent.v0.Capability_Type" json:"type,omitempty"`
}

func (x *Capability) Reset() {
	*x = Capability{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Capability) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Capability) ProtoMessage() {}

func (x *Capability) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[2]
	if x != nil {
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
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{2}
}

func (x *Capability) GetType() Capability_Type {
	if x != nil {
		return x.Type
	}
	return Capability_UNKNOWN
}

type Runtime struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type    Runtime_Type `protobuf:"varint,1,opt,name=type,proto3,enum=codefly.services.agent.v0.Runtime_Type" json:"type,omitempty"`
	Version string       `protobuf:"bytes,2,opt,name=version,proto3" json:"version,omitempty"`
}

func (x *Runtime) Reset() {
	*x = Runtime{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Runtime) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Runtime) ProtoMessage() {}

func (x *Runtime) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Runtime.ProtoReflect.Descriptor instead.
func (*Runtime) Descriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{3}
}

func (x *Runtime) GetType() Runtime_Type {
	if x != nil {
		return x.Type
	}
	return Runtime_UNKNOWN
}

func (x *Runtime) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

type ConfigurationValueInformation struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
}

func (x *ConfigurationValueInformation) Reset() {
	*x = ConfigurationValueInformation{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConfigurationValueInformation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigurationValueInformation) ProtoMessage() {}

func (x *ConfigurationValueInformation) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigurationValueInformation.ProtoReflect.Descriptor instead.
func (*ConfigurationValueInformation) Descriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{4}
}

func (x *ConfigurationValueInformation) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ConfigurationValueInformation) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

type ConfigurationValueDetail struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string                           `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description string                           `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	Fields      []*ConfigurationValueInformation `protobuf:"bytes,3,rep,name=fields,proto3" json:"fields,omitempty"`
}

func (x *ConfigurationValueDetail) Reset() {
	*x = ConfigurationValueDetail{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConfigurationValueDetail) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigurationValueDetail) ProtoMessage() {}

func (x *ConfigurationValueDetail) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigurationValueDetail.ProtoReflect.Descriptor instead.
func (*ConfigurationValueDetail) Descriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{5}
}

func (x *ConfigurationValueDetail) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ConfigurationValueDetail) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *ConfigurationValueDetail) GetFields() []*ConfigurationValueInformation {
	if x != nil {
		return x.Fields
	}
	return nil
}

type AgentInformation struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	RuntimeRequirements  []*Runtime                  `protobuf:"bytes,1,rep,name=runtime_requirements,json=runtimeRequirements,proto3" json:"runtime_requirements,omitempty"`
	Capabilities         []*Capability               `protobuf:"bytes,2,rep,name=capabilities,proto3" json:"capabilities,omitempty"`
	Protocols            []*Protocol                 `protobuf:"bytes,3,rep,name=protocols,proto3" json:"protocols,omitempty"`
	Languages            []*Language                 `protobuf:"bytes,4,rep,name=languages,proto3" json:"languages,omitempty"`
	ReadMe               string                      `protobuf:"bytes,5,opt,name=read_me,json=readMe,proto3" json:"read_me,omitempty"`
	ConfigurationDetails []*ConfigurationValueDetail `protobuf:"bytes,6,rep,name=configuration_details,json=configurationDetails,proto3" json:"configuration_details,omitempty"`
}

func (x *AgentInformation) Reset() {
	*x = AgentInformation{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *AgentInformation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AgentInformation) ProtoMessage() {}

func (x *AgentInformation) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[6]
	if x != nil {
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
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{6}
}

func (x *AgentInformation) GetRuntimeRequirements() []*Runtime {
	if x != nil {
		return x.RuntimeRequirements
	}
	return nil
}

func (x *AgentInformation) GetCapabilities() []*Capability {
	if x != nil {
		return x.Capabilities
	}
	return nil
}

func (x *AgentInformation) GetProtocols() []*Protocol {
	if x != nil {
		return x.Protocols
	}
	return nil
}

func (x *AgentInformation) GetLanguages() []*Language {
	if x != nil {
		return x.Languages
	}
	return nil
}

func (x *AgentInformation) GetReadMe() string {
	if x != nil {
		return x.ReadMe
	}
	return ""
}

func (x *AgentInformation) GetConfigurationDetails() []*ConfigurationValueDetail {
	if x != nil {
		return x.ConfigurationDetails
	}
	return nil
}

type AgentInformationRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *AgentInformationRequest) Reset() {
	*x = AgentInformationRequest{}
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *AgentInformationRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AgentInformationRequest) ProtoMessage() {}

func (x *AgentInformationRequest) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_agent_proto_msgTypes[7]
	if x != nil {
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
	return file_codefly_services_agent_v0_agent_proto_rawDescGZIP(), []int{7}
}

var File_codefly_services_agent_v0_agent_proto protoreflect.FileDescriptor

var file_codefly_services_agent_v0_agent_proto_rawDesc = []byte{
	0x0a, 0x25, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x73, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2f, 0x76, 0x30, 0x2f, 0x61, 0x67, 0x65, 0x6e,
	0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x19, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e,
	0x76, 0x30, 0x22, 0x8e, 0x01, 0x0a, 0x08, 0x4c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x12,
	0x3c, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x28, 0x2e,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x4c, 0x61, 0x6e, 0x67, 0x75, 0x61,
	0x67, 0x65, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x44, 0x0a,
	0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x06, 0x0a, 0x02, 0x47, 0x4f, 0x10, 0x00, 0x12, 0x0a, 0x0a,
	0x06, 0x50, 0x59, 0x54, 0x48, 0x4f, 0x4e, 0x10, 0x01, 0x12, 0x0e, 0x0a, 0x0a, 0x4a, 0x41, 0x56,
	0x41, 0x53, 0x43, 0x52, 0x49, 0x50, 0x54, 0x10, 0x02, 0x12, 0x0e, 0x0a, 0x0a, 0x54, 0x59, 0x50,
	0x45, 0x53, 0x43, 0x52, 0x49, 0x50, 0x54, 0x10, 0x03, 0x12, 0x08, 0x0a, 0x04, 0x52, 0x55, 0x42,
	0x59, 0x10, 0x04, 0x22, 0x64, 0x0a, 0x08, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x12,
	0x3c, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x28, 0x2e,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63,
	0x6f, 0x6c, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x1a, 0x0a,
	0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x08, 0x0a, 0x04, 0x48, 0x54, 0x54, 0x50, 0x10, 0x00, 0x12,
	0x08, 0x0a, 0x04, 0x47, 0x52, 0x50, 0x43, 0x10, 0x01, 0x22, 0x8b, 0x01, 0x0a, 0x0a, 0x43, 0x61,
	0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x12, 0x3e, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x2a, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e,
	0x76, 0x30, 0x2e, 0x43, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x54, 0x79,
	0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3d, 0x0a, 0x04, 0x54, 0x79, 0x70, 0x65,
	0x12, 0x0b, 0x0a, 0x07, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12, 0x0b, 0x0a,
	0x07, 0x42, 0x55, 0x49, 0x4c, 0x44, 0x45, 0x52, 0x10, 0x01, 0x12, 0x0b, 0x0a, 0x07, 0x52, 0x55,
	0x4e, 0x54, 0x49, 0x4d, 0x45, 0x10, 0x02, 0x12, 0x0e, 0x0a, 0x0a, 0x48, 0x4f, 0x54, 0x5f, 0x52,
	0x45, 0x4c, 0x4f, 0x41, 0x44, 0x10, 0x03, 0x22, 0xce, 0x01, 0x0a, 0x07, 0x52, 0x75, 0x6e, 0x74,
	0x69, 0x6d, 0x65, 0x12, 0x3b, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0e, 0x32, 0x27, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x52, 0x75,
	0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65,
	0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x22, 0x6c, 0x0a, 0x04, 0x54, 0x79,
	0x70, 0x65, 0x12, 0x0b, 0x0a, 0x07, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12,
	0x06, 0x0a, 0x02, 0x47, 0x4f, 0x10, 0x01, 0x12, 0x07, 0x0a, 0x03, 0x4e, 0x50, 0x4d, 0x10, 0x02,
	0x12, 0x0a, 0x0a, 0x06, 0x50, 0x59, 0x54, 0x48, 0x4f, 0x4e, 0x10, 0x03, 0x12, 0x11, 0x0a, 0x0d,
	0x50, 0x59, 0x54, 0x48, 0x4f, 0x4e, 0x5f, 0x50, 0x4f, 0x45, 0x54, 0x52, 0x59, 0x10, 0x04, 0x12,
	0x08, 0x0a, 0x04, 0x52, 0x55, 0x42, 0x59, 0x10, 0x05, 0x12, 0x0c, 0x0a, 0x08, 0x52, 0x55, 0x42,
	0x59, 0x5f, 0x47, 0x45, 0x4d, 0x10, 0x06, 0x12, 0x0f, 0x0a, 0x0b, 0x52, 0x55, 0x42, 0x59, 0x5f,
	0x42, 0x55, 0x4e, 0x44, 0x4c, 0x45, 0x10, 0x07, 0x22, 0x55, 0x0a, 0x1d, 0x43, 0x6f, 0x6e, 0x66,
	0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x49, 0x6e,
	0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d,
	0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a,
	0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x22,
	0xa2, 0x01, 0x0a, 0x18, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f,
	0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x12, 0x12, 0x0a, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69,
	0x6f, 0x6e, 0x12, 0x50, 0x0a, 0x06, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x73, 0x18, 0x03, 0x20, 0x03,
	0x28, 0x0b, 0x32, 0x38, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x43,
	0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75,
	0x65, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x06, 0x66, 0x69,
	0x65, 0x6c, 0x64, 0x73, 0x22, 0xbd, 0x03, 0x0a, 0x10, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e,
	0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x55, 0x0a, 0x14, 0x72, 0x75, 0x6e,
	0x74, 0x69, 0x6d, 0x65, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x6d, 0x65, 0x6e, 0x74,
	0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x22, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74,
	0x2e, 0x76, 0x30, 0x2e, 0x52, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x52, 0x13, 0x72, 0x75, 0x6e,
	0x74, 0x69, 0x6d, 0x65, 0x52, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x73,
	0x12, 0x49, 0x0a, 0x0c, 0x63, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x69, 0x65, 0x73,
	0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x25, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e,
	0x76, 0x30, 0x2e, 0x43, 0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x52, 0x0c, 0x63,
	0x61, 0x70, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x69, 0x65, 0x73, 0x12, 0x41, 0x0a, 0x09, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x23,
	0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x63, 0x6f, 0x6c, 0x52, 0x09, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x73, 0x12, 0x41,
	0x0a, 0x09, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x23, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x4c, 0x61,
	0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x52, 0x09, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65,
	0x73, 0x12, 0x17, 0x0a, 0x07, 0x72, 0x65, 0x61, 0x64, 0x5f, 0x6d, 0x65, 0x18, 0x05, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x06, 0x72, 0x65, 0x61, 0x64, 0x4d, 0x65, 0x12, 0x68, 0x0a, 0x15, 0x63, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x64, 0x65, 0x74, 0x61,
	0x69, 0x6c, 0x73, 0x18, 0x06, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x33, 0x2e, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65,
	0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x52, 0x14,
	0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x44, 0x65, 0x74,
	0x61, 0x69, 0x6c, 0x73, 0x22, 0x19, 0x0a, 0x17, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66,
	0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x32,
	0x81, 0x01, 0x0a, 0x05, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x12, 0x78, 0x0a, 0x13, 0x47, 0x65, 0x74,
	0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x12, 0x32, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x67, 0x65,
	0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x2b, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30,
	0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f,
	0x6e, 0x22, 0x00, 0x42, 0xf7, 0x01, 0x0a, 0x1d, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65,
	0x6e, 0x74, 0x2e, 0x76, 0x30, 0x42, 0x0a, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x50, 0x72, 0x6f, 0x74,
	0x6f, 0x50, 0x01, 0x5a, 0x42, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65,
	0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x61,
	0x67, 0x65, 0x6e, 0x74, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x04, 0x43, 0x53, 0x41, 0x56, 0xaa, 0x02,
	0x19, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x19, 0x43, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x5c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x41, 0x67,
	0x65, 0x6e, 0x74, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x25, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x5c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x5c,
	0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02,
	0x1c, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x73, 0x3a, 0x3a, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_services_agent_v0_agent_proto_rawDescOnce sync.Once
	file_codefly_services_agent_v0_agent_proto_rawDescData = file_codefly_services_agent_v0_agent_proto_rawDesc
)

func file_codefly_services_agent_v0_agent_proto_rawDescGZIP() []byte {
	file_codefly_services_agent_v0_agent_proto_rawDescOnce.Do(func() {
		file_codefly_services_agent_v0_agent_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_services_agent_v0_agent_proto_rawDescData)
	})
	return file_codefly_services_agent_v0_agent_proto_rawDescData
}

var file_codefly_services_agent_v0_agent_proto_enumTypes = make([]protoimpl.EnumInfo, 4)
var file_codefly_services_agent_v0_agent_proto_msgTypes = make([]protoimpl.MessageInfo, 8)
var file_codefly_services_agent_v0_agent_proto_goTypes = []any{
	(Language_Type)(0),                    // 0: codefly.services.agent.v0.Language.Type
	(Protocol_Type)(0),                    // 1: codefly.services.agent.v0.Protocol.Type
	(Capability_Type)(0),                  // 2: codefly.services.agent.v0.Capability.Type
	(Runtime_Type)(0),                     // 3: codefly.services.agent.v0.Runtime.Type
	(*Language)(nil),                      // 4: codefly.services.agent.v0.Language
	(*Protocol)(nil),                      // 5: codefly.services.agent.v0.Protocol
	(*Capability)(nil),                    // 6: codefly.services.agent.v0.Capability
	(*Runtime)(nil),                       // 7: codefly.services.agent.v0.Runtime
	(*ConfigurationValueInformation)(nil), // 8: codefly.services.agent.v0.ConfigurationValueInformation
	(*ConfigurationValueDetail)(nil),      // 9: codefly.services.agent.v0.ConfigurationValueDetail
	(*AgentInformation)(nil),              // 10: codefly.services.agent.v0.AgentInformation
	(*AgentInformationRequest)(nil),       // 11: codefly.services.agent.v0.AgentInformationRequest
}
var file_codefly_services_agent_v0_agent_proto_depIdxs = []int32{
	0,  // 0: codefly.services.agent.v0.Language.type:type_name -> codefly.services.agent.v0.Language.Type
	1,  // 1: codefly.services.agent.v0.Protocol.type:type_name -> codefly.services.agent.v0.Protocol.Type
	2,  // 2: codefly.services.agent.v0.Capability.type:type_name -> codefly.services.agent.v0.Capability.Type
	3,  // 3: codefly.services.agent.v0.Runtime.type:type_name -> codefly.services.agent.v0.Runtime.Type
	8,  // 4: codefly.services.agent.v0.ConfigurationValueDetail.fields:type_name -> codefly.services.agent.v0.ConfigurationValueInformation
	7,  // 5: codefly.services.agent.v0.AgentInformation.runtime_requirements:type_name -> codefly.services.agent.v0.Runtime
	6,  // 6: codefly.services.agent.v0.AgentInformation.capabilities:type_name -> codefly.services.agent.v0.Capability
	5,  // 7: codefly.services.agent.v0.AgentInformation.protocols:type_name -> codefly.services.agent.v0.Protocol
	4,  // 8: codefly.services.agent.v0.AgentInformation.languages:type_name -> codefly.services.agent.v0.Language
	9,  // 9: codefly.services.agent.v0.AgentInformation.configuration_details:type_name -> codefly.services.agent.v0.ConfigurationValueDetail
	11, // 10: codefly.services.agent.v0.Agent.GetAgentInformation:input_type -> codefly.services.agent.v0.AgentInformationRequest
	10, // 11: codefly.services.agent.v0.Agent.GetAgentInformation:output_type -> codefly.services.agent.v0.AgentInformation
	11, // [11:12] is the sub-list for method output_type
	10, // [10:11] is the sub-list for method input_type
	10, // [10:10] is the sub-list for extension type_name
	10, // [10:10] is the sub-list for extension extendee
	0,  // [0:10] is the sub-list for field type_name
}

func init() { file_codefly_services_agent_v0_agent_proto_init() }
func file_codefly_services_agent_v0_agent_proto_init() {
	if File_codefly_services_agent_v0_agent_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_codefly_services_agent_v0_agent_proto_rawDesc,
			NumEnums:      4,
			NumMessages:   8,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_codefly_services_agent_v0_agent_proto_goTypes,
		DependencyIndexes: file_codefly_services_agent_v0_agent_proto_depIdxs,
		EnumInfos:         file_codefly_services_agent_v0_agent_proto_enumTypes,
		MessageInfos:      file_codefly_services_agent_v0_agent_proto_msgTypes,
	}.Build()
	File_codefly_services_agent_v0_agent_proto = out.File
	file_codefly_services_agent_v0_agent_proto_rawDesc = nil
	file_codefly_services_agent_v0_agent_proto_goTypes = nil
	file_codefly_services_agent_v0_agent_proto_depIdxs = nil
}
