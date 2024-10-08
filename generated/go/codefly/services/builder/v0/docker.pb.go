// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/services/builder/v0/docker.proto

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

type DockerBuildContext struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DockerRepository string `protobuf:"bytes,1,opt,name=docker_repository,json=dockerRepository,proto3" json:"docker_repository,omitempty"`
}

func (x *DockerBuildContext) Reset() {
	*x = DockerBuildContext{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_services_builder_v0_docker_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DockerBuildContext) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DockerBuildContext) ProtoMessage() {}

func (x *DockerBuildContext) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_builder_v0_docker_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DockerBuildContext.ProtoReflect.Descriptor instead.
func (*DockerBuildContext) Descriptor() ([]byte, []int) {
	return file_codefly_services_builder_v0_docker_proto_rawDescGZIP(), []int{0}
}

func (x *DockerBuildContext) GetDockerRepository() string {
	if x != nil {
		return x.DockerRepository
	}
	return ""
}

type DockerBuildResult struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Images []string `protobuf:"bytes,1,rep,name=images,proto3" json:"images,omitempty"`
}

func (x *DockerBuildResult) Reset() {
	*x = DockerBuildResult{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_services_builder_v0_docker_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DockerBuildResult) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DockerBuildResult) ProtoMessage() {}

func (x *DockerBuildResult) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_services_builder_v0_docker_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DockerBuildResult.ProtoReflect.Descriptor instead.
func (*DockerBuildResult) Descriptor() ([]byte, []int) {
	return file_codefly_services_builder_v0_docker_proto_rawDescGZIP(), []int{1}
}

func (x *DockerBuildResult) GetImages() []string {
	if x != nil {
		return x.Images
	}
	return nil
}

var File_codefly_services_builder_v0_docker_proto protoreflect.FileDescriptor

var file_codefly_services_builder_v0_docker_proto_rawDesc = []byte{
	0x0a, 0x28, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x73, 0x2f, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x30, 0x2f, 0x64, 0x6f,
	0x63, 0x6b, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x1b, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x62, 0x75, 0x69,
	0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x22, 0x41, 0x0a, 0x12, 0x44, 0x6f, 0x63, 0x6b, 0x65,
	0x72, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x12, 0x2b, 0x0a,
	0x11, 0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72, 0x5f, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f,
	0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x10, 0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72,
	0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x22, 0x2b, 0x0a, 0x11, 0x44, 0x6f,
	0x63, 0x6b, 0x65, 0x72, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x52, 0x65, 0x73, 0x75, 0x6c, 0x74, 0x12,
	0x16, 0x0a, 0x06, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x06, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x73, 0x42, 0x84, 0x02, 0x0a, 0x1f, 0x63, 0x6f, 0x6d, 0x2e,
	0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x42, 0x0b, 0x44, 0x6f, 0x63,
	0x6b, 0x65, 0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x44, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64,
	0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65,
	0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x30,
	0xa2, 0x02, 0x04, 0x43, 0x53, 0x42, 0x56, 0xaa, 0x02, 0x1b, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x42, 0x75, 0x69, 0x6c, 0x64,
	0x65, 0x72, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x1b, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c,
	0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72,
	0x5c, 0x56, 0x30, 0xe2, 0x02, 0x27, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x5c, 0x56,
	0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x1e,
	0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x3a, 0x3a, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_services_builder_v0_docker_proto_rawDescOnce sync.Once
	file_codefly_services_builder_v0_docker_proto_rawDescData = file_codefly_services_builder_v0_docker_proto_rawDesc
)

func file_codefly_services_builder_v0_docker_proto_rawDescGZIP() []byte {
	file_codefly_services_builder_v0_docker_proto_rawDescOnce.Do(func() {
		file_codefly_services_builder_v0_docker_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_services_builder_v0_docker_proto_rawDescData)
	})
	return file_codefly_services_builder_v0_docker_proto_rawDescData
}

var file_codefly_services_builder_v0_docker_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_codefly_services_builder_v0_docker_proto_goTypes = []any{
	(*DockerBuildContext)(nil), // 0: codefly.services.builder.v0.DockerBuildContext
	(*DockerBuildResult)(nil),  // 1: codefly.services.builder.v0.DockerBuildResult
}
var file_codefly_services_builder_v0_docker_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_codefly_services_builder_v0_docker_proto_init() }
func file_codefly_services_builder_v0_docker_proto_init() {
	if File_codefly_services_builder_v0_docker_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_codefly_services_builder_v0_docker_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*DockerBuildContext); i {
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
		file_codefly_services_builder_v0_docker_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*DockerBuildResult); i {
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
			RawDescriptor: file_codefly_services_builder_v0_docker_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_services_builder_v0_docker_proto_goTypes,
		DependencyIndexes: file_codefly_services_builder_v0_docker_proto_depIdxs,
		MessageInfos:      file_codefly_services_builder_v0_docker_proto_msgTypes,
	}.Build()
	File_codefly_services_builder_v0_docker_proto = out.File
	file_codefly_services_builder_v0_docker_proto_rawDesc = nil
	file_codefly_services_builder_v0_docker_proto_goTypes = nil
	file_codefly_services_builder_v0_docker_proto_depIdxs = nil
}
