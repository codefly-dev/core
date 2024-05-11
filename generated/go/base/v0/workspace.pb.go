// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.1
// 	protoc        (unknown)
// source: base/v0/workspace.proto

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

// A Workspace is the top-level entity in the system.
// Services are organized in modules, and modules are part of a workspace.
// A workspace can itself be its own module for simple workspaces.
type Workspace struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// name is the name of the workspace
	// it needs to be a valid hostname, and it should not contain "--" as it will be used for DNS
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// description provides a brief explanation of the workspace.
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	// modules is the list of modules that are part of the workspace.
	Modules []*Module `protobuf:"bytes,3,rep,name=modules,proto3" json:"modules,omitempty"`
	// layout is how the workspace is structure
	Layout string `protobuf:"bytes,4,opt,name=layout,proto3" json:"layout,omitempty"`
}

func (x *Workspace) Reset() {
	*x = Workspace{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_workspace_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Workspace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Workspace) ProtoMessage() {}

func (x *Workspace) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_workspace_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Workspace.ProtoReflect.Descriptor instead.
func (*Workspace) Descriptor() ([]byte, []int) {
	return file_base_v0_workspace_proto_rawDescGZIP(), []int{0}
}

func (x *Workspace) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Workspace) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Workspace) GetModules() []*Module {
	if x != nil {
		return x.Modules
	}
	return nil
}

func (x *Workspace) GetLayout() string {
	if x != nil {
		return x.Layout
	}
	return ""
}

type ManagedWorkspace struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// organization ID is the unique identifier of the organization that owns the workspace.
	// UUID truncated to 10 characters.
	OrganizationId string `protobuf:"bytes,1,opt,name=organization_id,json=organizationId,proto3" json:"organization_id,omitempty"`
	// workspace ID is the unique identifier of the workspace.
	// UUID truncated to 10 characters.
	WorkspaceId string `protobuf:"bytes,2,opt,name=workspace_id,json=workspaceId,proto3" json:"workspace_id,omitempty"`
	// Workspace itself
	Workspace *Workspace `protobuf:"bytes,3,opt,name=workspace,proto3" json:"workspace,omitempty"`
}

func (x *ManagedWorkspace) Reset() {
	*x = ManagedWorkspace{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_workspace_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ManagedWorkspace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ManagedWorkspace) ProtoMessage() {}

func (x *ManagedWorkspace) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_workspace_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ManagedWorkspace.ProtoReflect.Descriptor instead.
func (*ManagedWorkspace) Descriptor() ([]byte, []int) {
	return file_base_v0_workspace_proto_rawDescGZIP(), []int{1}
}

func (x *ManagedWorkspace) GetOrganizationId() string {
	if x != nil {
		return x.OrganizationId
	}
	return ""
}

func (x *ManagedWorkspace) GetWorkspaceId() string {
	if x != nil {
		return x.WorkspaceId
	}
	return ""
}

func (x *ManagedWorkspace) GetWorkspace() *Workspace {
	if x != nil {
		return x.Workspace
	}
	return nil
}

var File_base_v0_workspace_proto protoreflect.FileDescriptor

var file_base_v0_workspace_proto_rawDesc = []byte{
	0x0a, 0x17, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x77, 0x6f, 0x72, 0x6b, 0x73, 0x70,
	0x61, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x62, 0x61, 0x73, 0x65, 0x2e,
	0x76, 0x30, 0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65,
	0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x14, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xc2, 0x01, 0x0a, 0x09, 0x57, 0x6f, 0x72, 0x6b, 0x73, 0x70,
	0x61, 0x63, 0x65, 0x12, 0x32, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x42, 0x1e, 0xba, 0x48, 0x1b, 0x72, 0x19, 0x10, 0x03, 0x18, 0x19, 0x32, 0x0c, 0x5e, 0x5b,
	0x61, 0x2d, 0x7a, 0x30, 0x2d, 0x39, 0x2d, 0x5d, 0x2b, 0x24, 0xba, 0x01, 0x02, 0x2d, 0x2d, 0x68,
	0x01, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63, 0x72,
	0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64, 0x65,
	0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x29, 0x0a, 0x07, 0x6d, 0x6f, 0x64,
	0x75, 0x6c, 0x65, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0f, 0x2e, 0x62, 0x61, 0x73,
	0x65, 0x2e, 0x76, 0x30, 0x2e, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x52, 0x07, 0x6d, 0x6f, 0x64,
	0x75, 0x6c, 0x65, 0x73, 0x12, 0x34, 0x0a, 0x06, 0x6c, 0x61, 0x79, 0x6f, 0x75, 0x74, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x09, 0x42, 0x1c, 0xba, 0x48, 0x19, 0x72, 0x17, 0x52, 0x04, 0x66, 0x6c, 0x61,
	0x74, 0x52, 0x07, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x73, 0x52, 0x06, 0x68, 0x79, 0x62, 0x72,
	0x69, 0x64, 0x52, 0x06, 0x6c, 0x61, 0x79, 0x6f, 0x75, 0x74, 0x22, 0xb8, 0x01, 0x0a, 0x10, 0x4d,
	0x61, 0x6e, 0x61, 0x67, 0x65, 0x64, 0x57, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12,
	0x3b, 0x0a, 0x0f, 0x6f, 0x72, 0x67, 0x61, 0x6e, 0x69, 0x7a, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f,
	0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x42, 0x12, 0xba, 0x48, 0x0f, 0x72, 0x0d, 0x32,
	0x0b, 0x5e, 0x5b, 0x61, 0x2d, 0x7a, 0x5d, 0x7b, 0x31, 0x30, 0x7d, 0x24, 0x52, 0x0e, 0x6f, 0x72,
	0x67, 0x61, 0x6e, 0x69, 0x7a, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x64, 0x12, 0x35, 0x0a, 0x0c,
	0x77, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x42, 0x12, 0xba, 0x48, 0x0f, 0x72, 0x0d, 0x32, 0x0b, 0x5e, 0x5b, 0x61, 0x2d, 0x7a,
	0x5d, 0x7b, 0x31, 0x30, 0x7d, 0x24, 0x52, 0x0b, 0x77, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63,
	0x65, 0x49, 0x64, 0x12, 0x30, 0x0a, 0x09, 0x77, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63, 0x65,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x2e, 0x57, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63, 0x65, 0x52, 0x09, 0x77, 0x6f, 0x72, 0x6b,
	0x73, 0x70, 0x61, 0x63, 0x65, 0x42, 0x8c, 0x01, 0x0a, 0x0b, 0x63, 0x6f, 0x6d, 0x2e, 0x62, 0x61,
	0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x0e, 0x57, 0x6f, 0x72, 0x6b, 0x73, 0x70, 0x61, 0x63, 0x65,
	0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x30, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e,
	0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f,
	0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67,
	0x6f, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x42, 0x56, 0x58, 0xaa,
	0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65,
	0x5c, 0x56, 0x30, 0xe2, 0x02, 0x13, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50,
	0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x08, 0x42, 0x61, 0x73, 0x65,
	0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_base_v0_workspace_proto_rawDescOnce sync.Once
	file_base_v0_workspace_proto_rawDescData = file_base_v0_workspace_proto_rawDesc
)

func file_base_v0_workspace_proto_rawDescGZIP() []byte {
	file_base_v0_workspace_proto_rawDescOnce.Do(func() {
		file_base_v0_workspace_proto_rawDescData = protoimpl.X.CompressGZIP(file_base_v0_workspace_proto_rawDescData)
	})
	return file_base_v0_workspace_proto_rawDescData
}

var file_base_v0_workspace_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_base_v0_workspace_proto_goTypes = []interface{}{
	(*Workspace)(nil),        // 0: base.v0.Workspace
	(*ManagedWorkspace)(nil), // 1: base.v0.ManagedWorkspace
	(*Module)(nil),           // 2: base.v0.Module
}
var file_base_v0_workspace_proto_depIdxs = []int32{
	2, // 0: base.v0.Workspace.modules:type_name -> base.v0.Module
	0, // 1: base.v0.ManagedWorkspace.workspace:type_name -> base.v0.Workspace
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_base_v0_workspace_proto_init() }
func file_base_v0_workspace_proto_init() {
	if File_base_v0_workspace_proto != nil {
		return
	}
	file_base_v0_module_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_base_v0_workspace_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Workspace); i {
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
		file_base_v0_workspace_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ManagedWorkspace); i {
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
			RawDescriptor: file_base_v0_workspace_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_base_v0_workspace_proto_goTypes,
		DependencyIndexes: file_base_v0_workspace_proto_depIdxs,
		MessageInfos:      file_base_v0_workspace_proto_msgTypes,
	}.Build()
	File_base_v0_workspace_proto = out.File
	file_base_v0_workspace_proto_rawDesc = nil
	file_base_v0_workspace_proto_goTypes = nil
	file_base_v0_workspace_proto_depIdxs = nil
}