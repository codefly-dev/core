// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.33.0
// 	protoc        (unknown)
// source: base/v0/project.proto

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

// A Project is the top-level entity in the system. It represents a collection of applications and their associated resources.
type Project struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// name is the name of the project
	// it needs to be a valid hostname, and it should not contain "--" as it will be used for DNS
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// description provides a brief explanation of the project.
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	// applications is a list of applications associated with the project.
	Applications []*Application `protobuf:"bytes,3,rep,name=applications,proto3" json:"applications,omitempty"`
}

func (x *Project) Reset() {
	*x = Project{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_project_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Project) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Project) ProtoMessage() {}

func (x *Project) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_project_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Project.ProtoReflect.Descriptor instead.
func (*Project) Descriptor() ([]byte, []int) {
	return file_base_v0_project_proto_rawDescGZIP(), []int{0}
}

func (x *Project) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Project) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Project) GetApplications() []*Application {
	if x != nil {
		return x.Applications
	}
	return nil
}

type ManagedProject struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// organization ID is the unique identifier of the organization that owns the project.
	// UUID truncated to 10 characters.
	OrganizationId string `protobuf:"bytes,1,opt,name=organization_id,json=organizationId,proto3" json:"organization_id,omitempty"`
	// project ID is the unique identifier of the project.
	// UUID truncated to 10 characters.
	ProjectId string `protobuf:"bytes,2,opt,name=project_id,json=projectId,proto3" json:"project_id,omitempty"`
	// Project itself
	Project *Project `protobuf:"bytes,3,opt,name=project,proto3" json:"project,omitempty"`
}

func (x *ManagedProject) Reset() {
	*x = ManagedProject{}
	if protoimpl.UnsafeEnabled {
		mi := &file_base_v0_project_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ManagedProject) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ManagedProject) ProtoMessage() {}

func (x *ManagedProject) ProtoReflect() protoreflect.Message {
	mi := &file_base_v0_project_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ManagedProject.ProtoReflect.Descriptor instead.
func (*ManagedProject) Descriptor() ([]byte, []int) {
	return file_base_v0_project_proto_rawDescGZIP(), []int{1}
}

func (x *ManagedProject) GetOrganizationId() string {
	if x != nil {
		return x.OrganizationId
	}
	return ""
}

func (x *ManagedProject) GetProjectId() string {
	if x != nil {
		return x.ProjectId
	}
	return ""
}

func (x *ManagedProject) GetProject() *Project {
	if x != nil {
		return x.Project
	}
	return nil
}

var File_base_v0_project_proto protoreflect.FileDescriptor

var file_base_v0_project_proto_rawDesc = []byte{
	0x0a, 0x15, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63,
	0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2f, 0x76,
	0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x19, 0x62,
	0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0x2f, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69,
	0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x99, 0x01, 0x0a, 0x07, 0x50, 0x72, 0x6f,
	0x6a, 0x65, 0x63, 0x74, 0x12, 0x32, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x42, 0x1e, 0xba, 0x48, 0x1b, 0x72, 0x19, 0x10, 0x03, 0x18, 0x19, 0x32, 0x0c, 0x5e,
	0x5b, 0x61, 0x2d, 0x7a, 0x30, 0x2d, 0x39, 0x2d, 0x5d, 0x2b, 0x24, 0xba, 0x01, 0x02, 0x2d, 0x2d,
	0x68, 0x01, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63,
	0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64,
	0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x38, 0x0a, 0x0c, 0x61, 0x70,
	0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x14, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x0c, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x73, 0x22, 0xac, 0x01, 0x0a, 0x0e, 0x4d, 0x61, 0x6e, 0x61, 0x67, 0x65, 0x64,
	0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x3b, 0x0a, 0x0f, 0x6f, 0x72, 0x67, 0x61, 0x6e,
	0x69, 0x7a, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x42, 0x12, 0xba, 0x48, 0x0f, 0x72, 0x0d, 0x32, 0x0b, 0x5e, 0x5b, 0x61, 0x2d, 0x7a, 0x5d, 0x7b,
	0x31, 0x30, 0x7d, 0x24, 0x52, 0x0e, 0x6f, 0x72, 0x67, 0x61, 0x6e, 0x69, 0x7a, 0x61, 0x74, 0x69,
	0x6f, 0x6e, 0x49, 0x64, 0x12, 0x31, 0x0a, 0x0a, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x5f,
	0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x42, 0x12, 0xba, 0x48, 0x0f, 0x72, 0x0d, 0x32,
	0x0b, 0x5e, 0x5b, 0x61, 0x2d, 0x7a, 0x5d, 0x7b, 0x31, 0x30, 0x7d, 0x24, 0x52, 0x09, 0x70, 0x72,
	0x6f, 0x6a, 0x65, 0x63, 0x74, 0x49, 0x64, 0x12, 0x2a, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65,
	0x63, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e,
	0x76, 0x30, 0x2e, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a,
	0x65, 0x63, 0x74, 0x42, 0x8a, 0x01, 0x0a, 0x0b, 0x63, 0x6f, 0x6d, 0x2e, 0x62, 0x61, 0x73, 0x65,
	0x2e, 0x76, 0x30, 0x42, 0x0c, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x72, 0x6f, 0x74,
	0x6f, 0x50, 0x01, 0x5a, 0x30, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65,
	0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x62, 0x61,
	0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x42, 0x56, 0x58, 0xaa, 0x02, 0x07, 0x42, 0x61,
	0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x07, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0xe2,
	0x02, 0x13, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x08, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a, 0x56, 0x30,
	0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_base_v0_project_proto_rawDescOnce sync.Once
	file_base_v0_project_proto_rawDescData = file_base_v0_project_proto_rawDesc
)

func file_base_v0_project_proto_rawDescGZIP() []byte {
	file_base_v0_project_proto_rawDescOnce.Do(func() {
		file_base_v0_project_proto_rawDescData = protoimpl.X.CompressGZIP(file_base_v0_project_proto_rawDescData)
	})
	return file_base_v0_project_proto_rawDescData
}

var file_base_v0_project_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_base_v0_project_proto_goTypes = []interface{}{
	(*Project)(nil),        // 0: base.v0.Project
	(*ManagedProject)(nil), // 1: base.v0.ManagedProject
	(*Application)(nil),    // 2: base.v0.Application
}
var file_base_v0_project_proto_depIdxs = []int32{
	2, // 0: base.v0.Project.applications:type_name -> base.v0.Application
	0, // 1: base.v0.ManagedProject.project:type_name -> base.v0.Project
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_base_v0_project_proto_init() }
func file_base_v0_project_proto_init() {
	if File_base_v0_project_proto != nil {
		return
	}
	file_base_v0_application_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_base_v0_project_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Project); i {
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
		file_base_v0_project_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ManagedProject); i {
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
			RawDescriptor: file_base_v0_project_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_base_v0_project_proto_goTypes,
		DependencyIndexes: file_base_v0_project_proto_depIdxs,
		MessageInfos:      file_base_v0_project_proto_msgTypes,
	}.Build()
	File_base_v0_project_proto = out.File
	file_base_v0_project_proto_rawDesc = nil
	file_base_v0_project_proto_goTypes = nil
	file_base_v0_project_proto_depIdxs = nil
}
