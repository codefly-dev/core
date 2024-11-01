// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        (unknown)
// source: codefly/observability/v0/logs.proto

package v0

import (
	reflect "reflect"
	sync "sync"

	_ "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Log struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	At      *timestamppb.Timestamp `protobuf:"bytes,1,opt,name=at,proto3" json:"at,omitempty"`
	Module  string                 `protobuf:"bytes,2,opt,name=module,proto3" json:"module,omitempty"`
	Service string                 `protobuf:"bytes,3,opt,name=service,proto3" json:"service,omitempty"`
	Kind    string                 `protobuf:"bytes,4,opt,name=kind,proto3" json:"kind,omitempty"`
	Message string                 `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *Log) Reset() {
	*x = Log{}
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Log) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Log) ProtoMessage() {}

func (x *Log) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Log.ProtoReflect.Descriptor instead.
func (*Log) Descriptor() ([]byte, []int) {
	return file_codefly_observability_v0_logs_proto_rawDescGZIP(), []int{0}
}

func (x *Log) GetAt() *timestamppb.Timestamp {
	if x != nil {
		return x.At
	}
	return nil
}

func (x *Log) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *Log) GetService() string {
	if x != nil {
		return x.Service
	}
	return ""
}

func (x *Log) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *Log) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

type LogSessionGroup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Session *Session `protobuf:"bytes,1,opt,name=session,proto3" json:"session,omitempty"`
	Logs    []*Log   `protobuf:"bytes,2,rep,name=logs,proto3" json:"logs,omitempty"`
}

func (x *LogSessionGroup) Reset() {
	*x = LogSessionGroup{}
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LogSessionGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogSessionGroup) ProtoMessage() {}

func (x *LogSessionGroup) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogSessionGroup.ProtoReflect.Descriptor instead.
func (*LogSessionGroup) Descriptor() ([]byte, []int) {
	return file_codefly_observability_v0_logs_proto_rawDescGZIP(), []int{1}
}

func (x *LogSessionGroup) GetSession() *Session {
	if x != nil {
		return x.Session
	}
	return nil
}

func (x *LogSessionGroup) GetLogs() []*Log {
	if x != nil {
		return x.Logs
	}
	return nil
}

type LogRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	From *timestamppb.Timestamp `protobuf:"bytes,1,opt,name=from,proto3" json:"from,omitempty"`
	To   *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=to,proto3" json:"to,omitempty"`
}

func (x *LogRequest) Reset() {
	*x = LogRequest{}
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LogRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogRequest) ProtoMessage() {}

func (x *LogRequest) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogRequest.ProtoReflect.Descriptor instead.
func (*LogRequest) Descriptor() ([]byte, []int) {
	return file_codefly_observability_v0_logs_proto_rawDescGZIP(), []int{2}
}

func (x *LogRequest) GetFrom() *timestamppb.Timestamp {
	if x != nil {
		return x.From
	}
	return nil
}

func (x *LogRequest) GetTo() *timestamppb.Timestamp {
	if x != nil {
		return x.To
	}
	return nil
}

type LogResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Groups []*LogSessionGroup `protobuf:"bytes,1,rep,name=groups,proto3" json:"groups,omitempty"`
}

func (x *LogResponse) Reset() {
	*x = LogResponse{}
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LogResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogResponse) ProtoMessage() {}

func (x *LogResponse) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_observability_v0_logs_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogResponse.ProtoReflect.Descriptor instead.
func (*LogResponse) Descriptor() ([]byte, []int) {
	return file_codefly_observability_v0_logs_proto_rawDescGZIP(), []int{3}
}

func (x *LogResponse) GetGroups() []*LogSessionGroup {
	if x != nil {
		return x.Groups
	}
	return nil
}

var File_codefly_observability_v0_logs_proto protoreflect.FileDescriptor

var file_codefly_observability_v0_logs_proto_rawDesc = []byte{
	0x0a, 0x23, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76,
	0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f, 0x76, 0x30, 0x2f, 0x6c, 0x6f, 0x67, 0x73, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x18, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x6f,
	0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x1a,
	0x1e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76, 0x30,
	0x2f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x27, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61,
	0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f, 0x76, 0x30, 0x2f, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f,
	0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74,
	0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x91, 0x01, 0x0a, 0x03, 0x4c, 0x6f,
	0x67, 0x12, 0x2a, 0x0a, 0x02, 0x61, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x02, 0x61, 0x74, 0x12, 0x16, 0x0a,
	0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6d,
	0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12,
	0x12, 0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6b,
	0x69, 0x6e, 0x64, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x05,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x81, 0x01,
	0x0a, 0x0f, 0x4c, 0x6f, 0x67, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x47, 0x72, 0x6f, 0x75,
	0x70, 0x12, 0x3b, 0x0a, 0x07, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x21, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x6f, 0x62, 0x73,
	0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x53, 0x65,
	0x73, 0x73, 0x69, 0x6f, 0x6e, 0x52, 0x07, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x12, 0x31,
	0x0a, 0x04, 0x6c, 0x6f, 0x67, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1d, 0x2e, 0x63,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69,
	0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x4c, 0x6f, 0x67, 0x52, 0x04, 0x6c, 0x6f, 0x67,
	0x73, 0x22, 0x68, 0x0a, 0x0a, 0x4c, 0x6f, 0x67, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12,
	0x2e, 0x0a, 0x04, 0x66, 0x72, 0x6f, 0x6d, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x04, 0x66, 0x72, 0x6f, 0x6d, 0x12,
	0x2a, 0x0a, 0x02, 0x74, 0x6f, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x02, 0x74, 0x6f, 0x22, 0x50, 0x0a, 0x0b, 0x4c,
	0x6f, 0x67, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x41, 0x0a, 0x06, 0x67, 0x72,
	0x6f, 0x75, 0x70, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x29, 0x2e, 0x63, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69,
	0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x4c, 0x6f, 0x67, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e,
	0x47, 0x72, 0x6f, 0x75, 0x70, 0x52, 0x06, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x73, 0x42, 0xee, 0x01,
	0x0a, 0x1c, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x6f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x42, 0x09,
	0x4c, 0x6f, 0x67, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x41, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d,
	0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74,
	0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x6f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f, 0x76, 0x30, 0xa2, 0x02,
	0x03, 0x43, 0x4f, 0x56, 0xaa, 0x02, 0x18, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x4f,
	0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x56, 0x30, 0xca,
	0x02, 0x18, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76,
	0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x24, 0x43, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x5c, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69,
	0x74, 0x79, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0xea, 0x02, 0x1a, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x4f, 0x62, 0x73,
	0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_observability_v0_logs_proto_rawDescOnce sync.Once
	file_codefly_observability_v0_logs_proto_rawDescData = file_codefly_observability_v0_logs_proto_rawDesc
)

func file_codefly_observability_v0_logs_proto_rawDescGZIP() []byte {
	file_codefly_observability_v0_logs_proto_rawDescOnce.Do(func() {
		file_codefly_observability_v0_logs_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_observability_v0_logs_proto_rawDescData)
	})
	return file_codefly_observability_v0_logs_proto_rawDescData
}

var file_codefly_observability_v0_logs_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_codefly_observability_v0_logs_proto_goTypes = []any{
	(*Log)(nil),                   // 0: codefly.observability.v0.Log
	(*LogSessionGroup)(nil),       // 1: codefly.observability.v0.LogSessionGroup
	(*LogRequest)(nil),            // 2: codefly.observability.v0.LogRequest
	(*LogResponse)(nil),           // 3: codefly.observability.v0.LogResponse
	(*timestamppb.Timestamp)(nil), // 4: google.protobuf.Timestamp
	(*Session)(nil),               // 5: codefly.observability.v0.Session
}
var file_codefly_observability_v0_logs_proto_depIdxs = []int32{
	4, // 0: codefly.observability.v0.Log.at:type_name -> google.protobuf.Timestamp
	5, // 1: codefly.observability.v0.LogSessionGroup.session:type_name -> codefly.observability.v0.Session
	0, // 2: codefly.observability.v0.LogSessionGroup.logs:type_name -> codefly.observability.v0.Log
	4, // 3: codefly.observability.v0.LogRequest.from:type_name -> google.protobuf.Timestamp
	4, // 4: codefly.observability.v0.LogRequest.to:type_name -> google.protobuf.Timestamp
	1, // 5: codefly.observability.v0.LogResponse.groups:type_name -> codefly.observability.v0.LogSessionGroup
	6, // [6:6] is the sub-list for method output_type
	6, // [6:6] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_codefly_observability_v0_logs_proto_init() }
func file_codefly_observability_v0_logs_proto_init() {
	if File_codefly_observability_v0_logs_proto != nil {
		return
	}
	file_codefly_observability_v0_sessions_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_codefly_observability_v0_logs_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_observability_v0_logs_proto_goTypes,
		DependencyIndexes: file_codefly_observability_v0_logs_proto_depIdxs,
		MessageInfos:      file_codefly_observability_v0_logs_proto_msgTypes,
	}.Build()
	File_codefly_observability_v0_logs_proto = out.File
	file_codefly_observability_v0_logs_proto_rawDesc = nil
	file_codefly_observability_v0_logs_proto_goTypes = nil
	file_codefly_observability_v0_logs_proto_depIdxs = nil
}
