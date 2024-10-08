// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/base/v0/network.proto

package v0

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

// An endpoint to be accessed needs Network Mapping.
//
// # Network Mappings come with different scopes
//
// - FromContainer: This is the mapping we want to use inside Container
// - FromHost: This is the mapping we want to use from the Host
// - Public: This is the mapping we want to use if something is publicly accessible: match to a DNS record
type NetworkMapping struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The endpoint to be accessed
	Endpoint *Endpoint `protobuf:"bytes,1,opt,name=endpoint,proto3" json:"endpoint,omitempty"`
	// The network instances corresponding to the endpoint
	Instances []*NetworkInstance `protobuf:"bytes,2,rep,name=instances,proto3" json:"instances,omitempty"`
}

func (x *NetworkMapping) Reset() {
	*x = NetworkMapping{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_network_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NetworkMapping) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NetworkMapping) ProtoMessage() {}

func (x *NetworkMapping) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_network_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NetworkMapping.ProtoReflect.Descriptor instead.
func (*NetworkMapping) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_network_proto_rawDescGZIP(), []int{0}
}

func (x *NetworkMapping) GetEndpoint() *Endpoint {
	if x != nil {
		return x.Endpoint
	}
	return nil
}

func (x *NetworkMapping) GetInstances() []*NetworkInstance {
	if x != nil {
		return x.Instances
	}
	return nil
}

type DNS struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The name of the DNS record
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// The module name
	Module string `protobuf:"bytes,2,opt,name=module,proto3" json:"module,omitempty"`
	// The service name
	Service string `protobuf:"bytes,3,opt,name=service,proto3" json:"service,omitempty"`
	// The endpoint name
	Endpoint string `protobuf:"bytes,4,opt,name=endpoint,proto3" json:"endpoint,omitempty"`
	// The network instance name
	Host string `protobuf:"bytes,5,opt,name=host,proto3" json:"host,omitempty"`
	// The network instance port
	Port uint32 `protobuf:"varint,6,opt,name=port,proto3" json:"port,omitempty"`
	// Secured
	Secured bool `protobuf:"varint,7,opt,name=secured,proto3" json:"secured,omitempty"`
}

func (x *DNS) Reset() {
	*x = DNS{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_network_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DNS) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DNS) ProtoMessage() {}

func (x *DNS) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_network_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DNS.ProtoReflect.Descriptor instead.
func (*DNS) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_network_proto_rawDescGZIP(), []int{1}
}

func (x *DNS) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *DNS) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *DNS) GetService() string {
	if x != nil {
		return x.Service
	}
	return ""
}

func (x *DNS) GetEndpoint() string {
	if x != nil {
		return x.Endpoint
	}
	return ""
}

func (x *DNS) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *DNS) GetPort() uint32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *DNS) GetSecured() bool {
	if x != nil {
		return x.Secured
	}
	return false
}

type NetworkInstance struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The scope of the mapping
	Access *NetworkAccess `protobuf:"bytes,1,opt,name=access,proto3" json:"access,omitempty"`
	// The host for the instance
	Host string `protobuf:"bytes,3,opt,name=host,proto3" json:"host,omitempty"`
	// The hostname for the instance
	Hostname string `protobuf:"bytes,4,opt,name=hostname,proto3" json:"hostname,omitempty"`
	// The port for the instance
	Port uint32 `protobuf:"varint,5,opt,name=port,proto3" json:"port,omitempty"`
	// The address for the instance
	Address string `protobuf:"bytes,6,opt,name=address,proto3" json:"address,omitempty"`
}

func (x *NetworkInstance) Reset() {
	*x = NetworkInstance{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_network_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NetworkInstance) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NetworkInstance) ProtoMessage() {}

func (x *NetworkInstance) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_network_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NetworkInstance.ProtoReflect.Descriptor instead.
func (*NetworkInstance) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_network_proto_rawDescGZIP(), []int{2}
}

func (x *NetworkInstance) GetAccess() *NetworkAccess {
	if x != nil {
		return x.Access
	}
	return nil
}

func (x *NetworkInstance) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *NetworkInstance) GetHostname() string {
	if x != nil {
		return x.Hostname
	}
	return ""
}

func (x *NetworkInstance) GetPort() uint32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *NetworkInstance) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

var File_codefly_base_v0_network_proto protoreflect.FileDescriptor

var file_codefly_base_v0_network_proto_rawDesc = []byte{
	0x0a, 0x1d, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x0f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x1a, 0x1b, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x73, 0x63, 0x6f, 0x70, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1e, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x65,
	0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x87, 0x01,
	0x0a, 0x0e, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x4d, 0x61, 0x70, 0x70, 0x69, 0x6e, 0x67,
	0x12, 0x35, 0x0a, 0x08, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x19, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73,
	0x65, 0x2e, 0x76, 0x30, 0x2e, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x52, 0x08, 0x65,
	0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x12, 0x3e, 0x0a, 0x09, 0x69, 0x6e, 0x73, 0x74, 0x61,
	0x6e, 0x63, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x20, 0x2e, 0x63, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x4e, 0x65, 0x74,
	0x77, 0x6f, 0x72, 0x6b, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x52, 0x09, 0x69, 0x6e,
	0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x73, 0x22, 0xa9, 0x01, 0x0a, 0x03, 0x44, 0x4e, 0x53, 0x12,
	0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x73, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x1a, 0x0a, 0x08, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e,
	0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x68, 0x6f, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x06, 0x20,
	0x01, 0x28, 0x0d, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65, 0x63,
	0x75, 0x72, 0x65, 0x64, 0x18, 0x07, 0x20, 0x01, 0x28, 0x08, 0x52, 0x07, 0x73, 0x65, 0x63, 0x75,
	0x72, 0x65, 0x64, 0x22, 0xa7, 0x01, 0x0a, 0x0f, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x49,
	0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x12, 0x36, 0x0a, 0x06, 0x61, 0x63, 0x63, 0x65, 0x73,
	0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1e, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72,
	0x6b, 0x41, 0x63, 0x63, 0x65, 0x73, 0x73, 0x52, 0x06, 0x61, 0x63, 0x63, 0x65, 0x73, 0x73, 0x12,
	0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68,
	0x6f, 0x73, 0x74, 0x12, 0x1a, 0x0a, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x18,
	0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x12,
	0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x04, 0x70,
	0x6f, 0x72, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x06,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x42, 0xbb, 0x01,
	0x0a, 0x13, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61,
	0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x0c, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x50, 0x72,
	0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x38, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f,
	0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f,
	0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2,
	0x02, 0x03, 0x43, 0x42, 0x56, 0xaa, 0x02, 0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e,
	0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1b, 0x43, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d,
	0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x11, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x3a, 0x3a, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_codefly_base_v0_network_proto_rawDescOnce sync.Once
	file_codefly_base_v0_network_proto_rawDescData = file_codefly_base_v0_network_proto_rawDesc
)

func file_codefly_base_v0_network_proto_rawDescGZIP() []byte {
	file_codefly_base_v0_network_proto_rawDescOnce.Do(func() {
		file_codefly_base_v0_network_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_base_v0_network_proto_rawDescData)
	})
	return file_codefly_base_v0_network_proto_rawDescData
}

var file_codefly_base_v0_network_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_codefly_base_v0_network_proto_goTypes = []any{
	(*NetworkMapping)(nil),  // 0: codefly.base.v0.NetworkMapping
	(*DNS)(nil),             // 1: codefly.base.v0.DNS
	(*NetworkInstance)(nil), // 2: codefly.base.v0.NetworkInstance
	(*Endpoint)(nil),        // 3: codefly.base.v0.Endpoint
	(*NetworkAccess)(nil),   // 4: codefly.base.v0.NetworkAccess
}
var file_codefly_base_v0_network_proto_depIdxs = []int32{
	3, // 0: codefly.base.v0.NetworkMapping.endpoint:type_name -> codefly.base.v0.Endpoint
	2, // 1: codefly.base.v0.NetworkMapping.instances:type_name -> codefly.base.v0.NetworkInstance
	4, // 2: codefly.base.v0.NetworkInstance.access:type_name -> codefly.base.v0.NetworkAccess
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_codefly_base_v0_network_proto_init() }
func file_codefly_base_v0_network_proto_init() {
	if File_codefly_base_v0_network_proto != nil {
		return
	}
	file_codefly_base_v0_scope_proto_init()
	file_codefly_base_v0_endpoint_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_codefly_base_v0_network_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*NetworkMapping); i {
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
		file_codefly_base_v0_network_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*DNS); i {
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
		file_codefly_base_v0_network_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*NetworkInstance); i {
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
			RawDescriptor: file_codefly_base_v0_network_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_base_v0_network_proto_goTypes,
		DependencyIndexes: file_codefly_base_v0_network_proto_depIdxs,
		MessageInfos:      file_codefly_base_v0_network_proto_msgTypes,
	}.Build()
	File_codefly_base_v0_network_proto = out.File
	file_codefly_base_v0_network_proto_rawDesc = nil
	file_codefly_base_v0_network_proto_goTypes = nil
	file_codefly_base_v0_network_proto_depIdxs = nil
}
