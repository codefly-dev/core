// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        (unknown)
// source: codefly/services/agent/v0/cyclonedx.proto

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

type ComponentType int32

const (
	ComponentType_LIBRARY   ComponentType = 0
	ComponentType_FRAMEWORK ComponentType = 1
	ComponentType_MODULE    ComponentType = 2
	ComponentType_CONTAINER ComponentType = 3 // Add other component types
)

// Enum value maps for ComponentType.
var (
	ComponentType_name = map[int32]string{
		0: "LIBRARY",
		1: "FRAMEWORK",
		2: "MODULE",
		3: "CONTAINER",
	}
	ComponentType_value = map[string]int32{
		"LIBRARY":   0,
		"FRAMEWORK": 1,
		"MODULE":    2,
		"CONTAINER": 3,
	}
)

func (x ComponentType) Enum() *ComponentType {
	p := new(ComponentType)
	*p = x
	return p
}

func (x ComponentType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ComponentType) Descriptor() protoreflect.EnumDescriptor {
	return file_codefly_services_agent_v0_cyclonedx_proto_enumTypes[0].Descriptor()
}

func (ComponentType) Type() protoreflect.EnumType {
	return &file_codefly_services_agent_v0_cyclonedx_proto_enumTypes[0]
}

func (x ComponentType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ComponentType.Descriptor instead.
func (ComponentType) EnumDescriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_cyclonedx_proto_rawDescGZIP(), []int{0}
}

// Represents a component in the SBOM
type Component struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name    string        `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Version string        `protobuf:"bytes,2,opt,name=version,proto3" json:"version,omitempty"`
	Type    ComponentType `protobuf:"varint,3,opt,name=type,proto3,enum=codefly.services.agent.v0.ComponentType" json:"type,omitempty"`
	Group   string        `protobuf:"bytes,4,opt,name=group,proto3" json:"group,omitempty"` // Optional
	Purl    string        `protobuf:"bytes,5,opt,name=purl,proto3" json:"purl,omitempty"`   // Package URL
}

func (x *Component) Reset() {
	*x = Component{}
	mi := &file_codefly_services_agent_v0_cyclonedx_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Component) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Component) ProtoMessage() {}

func (x *Component) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_cyclonedx_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Component.ProtoReflect.Descriptor instead.
func (*Component) Descriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_cyclonedx_proto_rawDescGZIP(), []int{0}
}

func (x *Component) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Component) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

func (x *Component) GetType() ComponentType {
	if x != nil {
		return x.Type
	}
	return ComponentType_LIBRARY
}

func (x *Component) GetGroup() string {
	if x != nil {
		return x.Group
	}
	return ""
}

func (x *Component) GetPurl() string {
	if x != nil {
		return x.Purl
	}
	return ""
}

// Represents the entire SBOM
type Bom struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	BomFormat    string       `protobuf:"bytes,1,opt,name=bomFormat,proto3" json:"bomFormat,omitempty"` // Always "CycloneDX"
	SpecVersion  string       `protobuf:"bytes,2,opt,name=specVersion,proto3" json:"specVersion,omitempty"`
	SerialNumber string       `protobuf:"bytes,3,opt,name=serialNumber,proto3" json:"serialNumber,omitempty"`
	Version      int32        `protobuf:"varint,4,opt,name=version,proto3" json:"version,omitempty"`
	Components   []*Component `protobuf:"bytes,5,rep,name=components,proto3" json:"components,omitempty"` // Add other fields like metadata, dependencies, vulnerabilities, etc.
}

func (x *Bom) Reset() {
	*x = Bom{}
	mi := &file_codefly_services_agent_v0_cyclonedx_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Bom) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Bom) ProtoMessage() {}

func (x *Bom) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_agent_v0_cyclonedx_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Bom.ProtoReflect.Descriptor instead.
func (*Bom) Descriptor() ([]byte, []int) {
	return file_codefly_services_agent_v0_cyclonedx_proto_rawDescGZIP(), []int{1}
}

func (x *Bom) GetBomFormat() string {
	if x != nil {
		return x.BomFormat
	}
	return ""
}

func (x *Bom) GetSpecVersion() string {
	if x != nil {
		return x.SpecVersion
	}
	return ""
}

func (x *Bom) GetSerialNumber() string {
	if x != nil {
		return x.SerialNumber
	}
	return ""
}

func (x *Bom) GetVersion() int32 {
	if x != nil {
		return x.Version
	}
	return 0
}

func (x *Bom) GetComponents() []*Component {
	if x != nil {
		return x.Components
	}
	return nil
}

var File_codefly_services_agent_v0_cyclonedx_proto protoreflect.FileDescriptor

var file_codefly_services_agent_v0_cyclonedx_proto_rawDesc = []byte{
	0x0a, 0x29, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x73, 0x2f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2f, 0x76, 0x30, 0x2f, 0x63, 0x79, 0x63, 0x6c,
	0x6f, 0x6e, 0x65, 0x64, 0x78, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x19, 0x63, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67,
	0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x22, 0xa1, 0x01, 0x0a, 0x09, 0x43, 0x6f, 0x6d, 0x70, 0x6f,
	0x6e, 0x65, 0x6e, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72, 0x73,
	0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69,
	0x6f, 0x6e, 0x12, 0x3c, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0e,
	0x32, 0x28, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30, 0x2e, 0x43, 0x6f, 0x6d,
	0x70, 0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65,
	0x12, 0x14, 0x0a, 0x05, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x05, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x75, 0x72, 0x6c, 0x18, 0x05,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x70, 0x75, 0x72, 0x6c, 0x22, 0xc9, 0x01, 0x0a, 0x03, 0x42,
	0x6f, 0x6d, 0x12, 0x1c, 0x0a, 0x09, 0x62, 0x6f, 0x6d, 0x46, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x62, 0x6f, 0x6d, 0x46, 0x6f, 0x72, 0x6d, 0x61, 0x74,
	0x12, 0x20, 0x0a, 0x0b, 0x73, 0x70, 0x65, 0x63, 0x56, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x73, 0x70, 0x65, 0x63, 0x56, 0x65, 0x72, 0x73, 0x69,
	0x6f, 0x6e, 0x12, 0x22, 0x0a, 0x0c, 0x73, 0x65, 0x72, 0x69, 0x61, 0x6c, 0x4e, 0x75, 0x6d, 0x62,
	0x65, 0x72, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0c, 0x73, 0x65, 0x72, 0x69, 0x61, 0x6c,
	0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f,
	0x6e, 0x18, 0x04, 0x20, 0x01, 0x28, 0x05, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e,
	0x12, 0x44, 0x0a, 0x0a, 0x63, 0x6f, 0x6d, 0x70, 0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x73, 0x18, 0x05,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x24, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30,
	0x2e, 0x43, 0x6f, 0x6d, 0x70, 0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x52, 0x0a, 0x63, 0x6f, 0x6d, 0x70,
	0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x73, 0x2a, 0x46, 0x0a, 0x0d, 0x43, 0x6f, 0x6d, 0x70, 0x6f, 0x6e,
	0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x12, 0x0b, 0x0a, 0x07, 0x4c, 0x49, 0x42, 0x52, 0x41,
	0x52, 0x59, 0x10, 0x00, 0x12, 0x0d, 0x0a, 0x09, 0x46, 0x52, 0x41, 0x4d, 0x45, 0x57, 0x4f, 0x52,
	0x4b, 0x10, 0x01, 0x12, 0x0a, 0x0a, 0x06, 0x4d, 0x4f, 0x44, 0x55, 0x4c, 0x45, 0x10, 0x02, 0x12,
	0x0d, 0x0a, 0x09, 0x43, 0x4f, 0x4e, 0x54, 0x41, 0x49, 0x4e, 0x45, 0x52, 0x10, 0x03, 0x42, 0xfb,
	0x01, 0x0a, 0x1d, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x30,
	0x42, 0x0e, 0x43, 0x79, 0x63, 0x6c, 0x6f, 0x6e, 0x65, 0x64, 0x78, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x50, 0x01, 0x5a, 0x42, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f,
	0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x61, 0x67,
	0x65, 0x6e, 0x74, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x04, 0x43, 0x53, 0x41, 0x56, 0xaa, 0x02, 0x19,
	0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x19, 0x43, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x5c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x41, 0x67, 0x65,
	0x6e, 0x74, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x25, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c,
	0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x5c, 0x56,
	0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x1c,
	0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x3a, 0x3a, 0x41, 0x67, 0x65, 0x6e, 0x74, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_services_agent_v0_cyclonedx_proto_rawDescOnce sync.Once
	file_codefly_services_agent_v0_cyclonedx_proto_rawDescData = file_codefly_services_agent_v0_cyclonedx_proto_rawDesc
)

func file_codefly_services_agent_v0_cyclonedx_proto_rawDescGZIP() []byte {
	file_codefly_services_agent_v0_cyclonedx_proto_rawDescOnce.Do(func() {
		file_codefly_services_agent_v0_cyclonedx_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_services_agent_v0_cyclonedx_proto_rawDescData)
	})
	return file_codefly_services_agent_v0_cyclonedx_proto_rawDescData
}

var file_codefly_services_agent_v0_cyclonedx_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_codefly_services_agent_v0_cyclonedx_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_codefly_services_agent_v0_cyclonedx_proto_goTypes = []any{
	(ComponentType)(0), // 0: codefly.services.agent.v0.ComponentType
	(*Component)(nil),  // 1: codefly.services.agent.v0.Component
	(*Bom)(nil),        // 2: codefly.services.agent.v0.Bom
}
var file_codefly_services_agent_v0_cyclonedx_proto_depIdxs = []int32{
	0, // 0: codefly.services.agent.v0.Component.type:type_name -> codefly.services.agent.v0.ComponentType
	1, // 1: codefly.services.agent.v0.Bom.components:type_name -> codefly.services.agent.v0.Component
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_codefly_services_agent_v0_cyclonedx_proto_init() }
func file_codefly_services_agent_v0_cyclonedx_proto_init() {
	if File_codefly_services_agent_v0_cyclonedx_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_codefly_services_agent_v0_cyclonedx_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_services_agent_v0_cyclonedx_proto_goTypes,
		DependencyIndexes: file_codefly_services_agent_v0_cyclonedx_proto_depIdxs,
		EnumInfos:         file_codefly_services_agent_v0_cyclonedx_proto_enumTypes,
		MessageInfos:      file_codefly_services_agent_v0_cyclonedx_proto_msgTypes,
	}.Build()
	File_codefly_services_agent_v0_cyclonedx_proto = out.File
	file_codefly_services_agent_v0_cyclonedx_proto_rawDesc = nil
	file_codefly_services_agent_v0_cyclonedx_proto_goTypes = nil
	file_codefly_services_agent_v0_cyclonedx_proto_depIdxs = nil
}
