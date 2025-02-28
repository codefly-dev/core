// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        (unknown)
// source: codefly/cli/v0/utils.proto

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

type FileInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Path        string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	Content     []byte `protobuf:"bytes,2,opt,name=content,proto3" json:"content,omitempty"`
	IsDirectory bool   `protobuf:"varint,3,opt,name=is_directory,json=isDirectory,proto3" json:"is_directory,omitempty"`
}

func (x *FileInfo) Reset() {
	*x = FileInfo{}
	mi := &file_codefly_cli_v0_utils_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FileInfo) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FileInfo) ProtoMessage() {}

func (x *FileInfo) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_cli_v0_utils_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FileInfo.ProtoReflect.Descriptor instead.
func (*FileInfo) Descriptor() ([]byte, []int) {
	return file_codefly_cli_v0_utils_proto_rawDescGZIP(), []int{0}
}

func (x *FileInfo) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

func (x *FileInfo) GetContent() []byte {
	if x != nil {
		return x.Content
	}
	return nil
}

func (x *FileInfo) GetIsDirectory() bool {
	if x != nil {
		return x.IsDirectory
	}
	return false
}

type Directory struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Files []*FileInfo `protobuf:"bytes,1,rep,name=files,proto3" json:"files,omitempty"`
}

func (x *Directory) Reset() {
	*x = Directory{}
	mi := &file_codefly_cli_v0_utils_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Directory) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Directory) ProtoMessage() {}

func (x *Directory) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_cli_v0_utils_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Directory.ProtoReflect.Descriptor instead.
func (*Directory) Descriptor() ([]byte, []int) {
	return file_codefly_cli_v0_utils_proto_rawDescGZIP(), []int{1}
}

func (x *Directory) GetFiles() []*FileInfo {
	if x != nil {
		return x.Files
	}
	return nil
}

var File_codefly_cli_v0_utils_proto protoreflect.FileDescriptor

var file_codefly_cli_v0_utils_proto_rawDesc = []byte{
	0x0a, 0x1a, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x63, 0x6c, 0x69, 0x2f, 0x76, 0x30,
	0x2f, 0x75, 0x74, 0x69, 0x6c, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0e, 0x63, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x63, 0x6c, 0x69, 0x2e, 0x76, 0x30, 0x22, 0x5b, 0x0a, 0x08,
	0x46, 0x69, 0x6c, 0x65, 0x49, 0x6e, 0x66, 0x6f, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x61, 0x74, 0x68,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x70, 0x61, 0x74, 0x68, 0x12, 0x18, 0x0a, 0x07,
	0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x63,
	0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x69, 0x73, 0x5f, 0x64, 0x69, 0x72,
	0x65, 0x63, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0b, 0x69, 0x73,
	0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x6f, 0x72, 0x79, 0x22, 0x3b, 0x0a, 0x09, 0x44, 0x69, 0x72,
	0x65, 0x63, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x2e, 0x0a, 0x05, 0x66, 0x69, 0x6c, 0x65, 0x73, 0x18,
	0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x18, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e,
	0x63, 0x6c, 0x69, 0x2e, 0x76, 0x30, 0x2e, 0x46, 0x69, 0x6c, 0x65, 0x49, 0x6e, 0x66, 0x6f, 0x52,
	0x05, 0x66, 0x69, 0x6c, 0x65, 0x73, 0x42, 0xb3, 0x01, 0x0a, 0x12, 0x63, 0x6f, 0x6d, 0x2e, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x63, 0x6c, 0x69, 0x2e, 0x76, 0x30, 0x42, 0x0a, 0x55,
	0x74, 0x69, 0x6c, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x37, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d,
	0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74,
	0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x63, 0x6c,
	0x69, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x43, 0x43, 0x56, 0xaa, 0x02, 0x0e, 0x43, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2e, 0x43, 0x6c, 0x69, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x0e, 0x43, 0x6f,
	0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x43, 0x6c, 0x69, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1a, 0x43,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x43, 0x6c, 0x69, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50,
	0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x10, 0x43, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x43, 0x6c, 0x69, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_cli_v0_utils_proto_rawDescOnce sync.Once
	file_codefly_cli_v0_utils_proto_rawDescData = file_codefly_cli_v0_utils_proto_rawDesc
)

func file_codefly_cli_v0_utils_proto_rawDescGZIP() []byte {
	file_codefly_cli_v0_utils_proto_rawDescOnce.Do(func() {
		file_codefly_cli_v0_utils_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_cli_v0_utils_proto_rawDescData)
	})
	return file_codefly_cli_v0_utils_proto_rawDescData
}

var file_codefly_cli_v0_utils_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_codefly_cli_v0_utils_proto_goTypes = []any{
	(*FileInfo)(nil),  // 0: codefly.cli.v0.FileInfo
	(*Directory)(nil), // 1: codefly.cli.v0.Directory
}
var file_codefly_cli_v0_utils_proto_depIdxs = []int32{
	0, // 0: codefly.cli.v0.Directory.files:type_name -> codefly.cli.v0.FileInfo
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_codefly_cli_v0_utils_proto_init() }
func file_codefly_cli_v0_utils_proto_init() {
	if File_codefly_cli_v0_utils_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_codefly_cli_v0_utils_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_cli_v0_utils_proto_goTypes,
		DependencyIndexes: file_codefly_cli_v0_utils_proto_depIdxs,
		MessageInfos:      file_codefly_cli_v0_utils_proto_msgTypes,
	}.Build()
	File_codefly_cli_v0_utils_proto = out.File
	file_codefly_cli_v0_utils_proto_rawDesc = nil
	file_codefly_cli_v0_utils_proto_goTypes = nil
	file_codefly_cli_v0_utils_proto_depIdxs = nil
}
