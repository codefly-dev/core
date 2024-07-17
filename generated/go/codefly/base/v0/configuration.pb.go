// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/base/v0/configuration.proto

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

type ConfigurationValue struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Key    string `protobuf:"bytes,1,opt,name=key,proto3" json:"key,omitempty"`
	Value  string `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"`
	Secret bool   `protobuf:"varint,3,opt,name=secret,proto3" json:"secret,omitempty"`
}

func (x *ConfigurationValue) Reset() {
	*x = ConfigurationValue{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_configuration_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConfigurationValue) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigurationValue) ProtoMessage() {}

func (x *ConfigurationValue) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_configuration_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigurationValue.ProtoReflect.Descriptor instead.
func (*ConfigurationValue) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_configuration_proto_rawDescGZIP(), []int{0}
}

func (x *ConfigurationValue) GetKey() string {
	if x != nil {
		return x.Key
	}
	return ""
}

func (x *ConfigurationValue) GetValue() string {
	if x != nil {
		return x.Value
	}
	return ""
}

func (x *ConfigurationValue) GetSecret() bool {
	if x != nil {
		return x.Secret
	}
	return false
}

type ConfigurationData struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind    string `protobuf:"bytes,1,opt,name=kind,proto3" json:"kind,omitempty"`
	Content []byte `protobuf:"bytes,2,opt,name=content,proto3" json:"content,omitempty"`
	Secret  bool   `protobuf:"varint,3,opt,name=secret,proto3" json:"secret,omitempty"`
}

func (x *ConfigurationData) Reset() {
	*x = ConfigurationData{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_configuration_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConfigurationData) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigurationData) ProtoMessage() {}

func (x *ConfigurationData) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_configuration_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigurationData.ProtoReflect.Descriptor instead.
func (*ConfigurationData) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_configuration_proto_rawDescGZIP(), []int{1}
}

func (x *ConfigurationData) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *ConfigurationData) GetContent() []byte {
	if x != nil {
		return x.Content
	}
	return nil
}

func (x *ConfigurationData) GetSecret() bool {
	if x != nil {
		return x.Secret
	}
	return false
}

type ConfigurationInformation struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name                string                `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	ConfigurationValues []*ConfigurationValue `protobuf:"bytes,2,rep,name=configuration_values,json=configurationValues,proto3" json:"configuration_values,omitempty"`
	Data                *ConfigurationData    `protobuf:"bytes,3,opt,name=data,proto3" json:"data,omitempty"`
}

func (x *ConfigurationInformation) Reset() {
	*x = ConfigurationInformation{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_configuration_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConfigurationInformation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigurationInformation) ProtoMessage() {}

func (x *ConfigurationInformation) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_configuration_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigurationInformation.ProtoReflect.Descriptor instead.
func (*ConfigurationInformation) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_configuration_proto_rawDescGZIP(), []int{2}
}

func (x *ConfigurationInformation) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ConfigurationInformation) GetConfigurationValues() []*ConfigurationValue {
	if x != nil {
		return x.ConfigurationValues
	}
	return nil
}

func (x *ConfigurationInformation) GetData() *ConfigurationData {
	if x != nil {
		return x.Data
	}
	return nil
}

// Configuration can come from
// - workspace: origin is _workspace
// - service: origin is the service unique
// Information is a grouping of configuration values
type Configuration struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Origin         string                      `protobuf:"bytes,1,opt,name=origin,proto3" json:"origin,omitempty"`
	RuntimeContext *RuntimeContext             `protobuf:"bytes,2,opt,name=runtime_context,json=runtimeContext,proto3" json:"runtime_context,omitempty"`
	Infos          []*ConfigurationInformation `protobuf:"bytes,3,rep,name=infos,proto3" json:"infos,omitempty"`
}

func (x *Configuration) Reset() {
	*x = Configuration{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_configuration_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Configuration) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Configuration) ProtoMessage() {}

func (x *Configuration) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_configuration_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Configuration.ProtoReflect.Descriptor instead.
func (*Configuration) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_configuration_proto_rawDescGZIP(), []int{3}
}

func (x *Configuration) GetOrigin() string {
	if x != nil {
		return x.Origin
	}
	return ""
}

func (x *Configuration) GetRuntimeContext() *RuntimeContext {
	if x != nil {
		return x.RuntimeContext
	}
	return nil
}

func (x *Configuration) GetInfos() []*ConfigurationInformation {
	if x != nil {
		return x.Infos
	}
	return nil
}

var File_codefly_base_v0_configuration_proto protoreflect.FileDescriptor

var file_codefly_base_v0_configuration_proto_rawDesc = []byte{
	0x0a, 0x23, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62,
	0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x1a, 0x1b, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f,
	0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x63, 0x6f, 0x70, 0x65, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x22, 0x54, 0x0a, 0x12, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75,
	0x65, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x08, 0x52, 0x06, 0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0x22, 0x59, 0x0a, 0x11, 0x43, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x44, 0x61, 0x74, 0x61, 0x12, 0x12,
	0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b, 0x69,
	0x6e, 0x64, 0x12, 0x18, 0x0a, 0x07, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0c, 0x52, 0x07, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x12, 0x16, 0x0a, 0x06,
	0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x73, 0x65,
	0x63, 0x72, 0x65, 0x74, 0x22, 0xbe, 0x01, 0x0a, 0x18, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75,
	0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x69, 0x6f,
	0x6e, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x56, 0x0a, 0x14, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75,
	0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x18, 0x02, 0x20,
	0x03, 0x28, 0x0b, 0x32, 0x23, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61,
	0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x52, 0x13, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67,
	0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x12, 0x36, 0x0a,
	0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x22, 0x2e, 0x63, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x43, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x44, 0x61, 0x74, 0x61, 0x52,
	0x04, 0x64, 0x61, 0x74, 0x61, 0x22, 0xb2, 0x01, 0x0a, 0x0d, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67,
	0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x16, 0x0a, 0x06, 0x6f, 0x72, 0x69, 0x67, 0x69,
	0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x12,
	0x48, 0x0a, 0x0f, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x5f, 0x63, 0x6f, 0x6e, 0x74, 0x65,
	0x78, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1f, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x52, 0x75, 0x6e, 0x74, 0x69,
	0x6d, 0x65, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x52, 0x0e, 0x72, 0x75, 0x6e, 0x74, 0x69,
	0x6d, 0x65, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x12, 0x3f, 0x0a, 0x05, 0x69, 0x6e, 0x66,
	0x6f, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x29, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69,
	0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x52, 0x05, 0x69, 0x6e, 0x66, 0x6f, 0x73, 0x42, 0xc1, 0x01, 0x0a, 0x13, 0x63,
	0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e,
	0x76, 0x30, 0x42, 0x12, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f,
	0x6e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x38, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62,
	0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76,
	0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f,
	0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f,
	0x76, 0x30, 0xa2, 0x02, 0x03, 0x43, 0x42, 0x56, 0xaa, 0x02, 0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x2e, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x0f, 0x43, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1b, 0x43,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47,
	0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x11, 0x43, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_base_v0_configuration_proto_rawDescOnce sync.Once
	file_codefly_base_v0_configuration_proto_rawDescData = file_codefly_base_v0_configuration_proto_rawDesc
)

func file_codefly_base_v0_configuration_proto_rawDescGZIP() []byte {
	file_codefly_base_v0_configuration_proto_rawDescOnce.Do(func() {
		file_codefly_base_v0_configuration_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_base_v0_configuration_proto_rawDescData)
	})
	return file_codefly_base_v0_configuration_proto_rawDescData
}

var file_codefly_base_v0_configuration_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_codefly_base_v0_configuration_proto_goTypes = []any{
	(*ConfigurationValue)(nil),       // 0: codefly.base.v0.ConfigurationValue
	(*ConfigurationData)(nil),        // 1: codefly.base.v0.ConfigurationData
	(*ConfigurationInformation)(nil), // 2: codefly.base.v0.ConfigurationInformation
	(*Configuration)(nil),            // 3: codefly.base.v0.Configuration
	(*RuntimeContext)(nil),           // 4: codefly.base.v0.RuntimeContext
}
var file_codefly_base_v0_configuration_proto_depIdxs = []int32{
	0, // 0: codefly.base.v0.ConfigurationInformation.configuration_values:type_name -> codefly.base.v0.ConfigurationValue
	1, // 1: codefly.base.v0.ConfigurationInformation.data:type_name -> codefly.base.v0.ConfigurationData
	4, // 2: codefly.base.v0.Configuration.runtime_context:type_name -> codefly.base.v0.RuntimeContext
	2, // 3: codefly.base.v0.Configuration.infos:type_name -> codefly.base.v0.ConfigurationInformation
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_codefly_base_v0_configuration_proto_init() }
func file_codefly_base_v0_configuration_proto_init() {
	if File_codefly_base_v0_configuration_proto != nil {
		return
	}
	file_codefly_base_v0_scope_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_codefly_base_v0_configuration_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*ConfigurationValue); i {
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
		file_codefly_base_v0_configuration_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*ConfigurationData); i {
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
		file_codefly_base_v0_configuration_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*ConfigurationInformation); i {
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
		file_codefly_base_v0_configuration_proto_msgTypes[3].Exporter = func(v any, i int) any {
			switch v := v.(*Configuration); i {
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
			RawDescriptor: file_codefly_base_v0_configuration_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_base_v0_configuration_proto_goTypes,
		DependencyIndexes: file_codefly_base_v0_configuration_proto_depIdxs,
		MessageInfos:      file_codefly_base_v0_configuration_proto_msgTypes,
	}.Build()
	File_codefly_base_v0_configuration_proto = out.File
	file_codefly_base_v0_configuration_proto_rawDesc = nil
	file_codefly_base_v0_configuration_proto_goTypes = nil
	file_codefly_base_v0_configuration_proto_depIdxs = nil
}
