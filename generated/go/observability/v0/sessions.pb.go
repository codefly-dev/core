// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.33.0
// 	protoc        (unknown)
// source: observability/v0/sessions.proto

package v0

import (
	reflect "reflect"
	sync "sync"

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

type ProjectSnapshot struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Uuid string `protobuf:"bytes,1,opt,name=uuid,proto3" json:"uuid,omitempty"`
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

func (x *ProjectSnapshot) Reset() {
	*x = ProjectSnapshot{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_sessions_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ProjectSnapshot) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ProjectSnapshot) ProtoMessage() {}

func (x *ProjectSnapshot) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_sessions_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ProjectSnapshot.ProtoReflect.Descriptor instead.
func (*ProjectSnapshot) Descriptor() ([]byte, []int) {
	return file_observability_v0_sessions_proto_rawDescGZIP(), []int{0}
}

func (x *ProjectSnapshot) GetUuid() string {
	if x != nil {
		return x.Uuid
	}
	return ""
}

func (x *ProjectSnapshot) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type ApplicationSnapshot struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Uuid    string           `protobuf:"bytes,1,opt,name=uuid,proto3" json:"uuid,omitempty"`
	Name    string           `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Project *ProjectSnapshot `protobuf:"bytes,3,opt,name=project,proto3" json:"project,omitempty"`
}

func (x *ApplicationSnapshot) Reset() {
	*x = ApplicationSnapshot{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_sessions_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApplicationSnapshot) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApplicationSnapshot) ProtoMessage() {}

func (x *ApplicationSnapshot) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_sessions_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApplicationSnapshot.ProtoReflect.Descriptor instead.
func (*ApplicationSnapshot) Descriptor() ([]byte, []int) {
	return file_observability_v0_sessions_proto_rawDescGZIP(), []int{1}
}

func (x *ApplicationSnapshot) GetUuid() string {
	if x != nil {
		return x.Uuid
	}
	return ""
}

func (x *ApplicationSnapshot) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ApplicationSnapshot) GetProject() *ProjectSnapshot {
	if x != nil {
		return x.Project
	}
	return nil
}

type PartialSnapshot struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Uuid         string                 `protobuf:"bytes,1,opt,name=uuid,proto3" json:"uuid,omitempty"`
	Name         string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Project      *ProjectSnapshot       `protobuf:"bytes,3,opt,name=project,proto3" json:"project,omitempty"`
	Applications []*ApplicationSnapshot `protobuf:"bytes,4,rep,name=applications,proto3" json:"applications,omitempty"`
}

func (x *PartialSnapshot) Reset() {
	*x = PartialSnapshot{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_sessions_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PartialSnapshot) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PartialSnapshot) ProtoMessage() {}

func (x *PartialSnapshot) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_sessions_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PartialSnapshot.ProtoReflect.Descriptor instead.
func (*PartialSnapshot) Descriptor() ([]byte, []int) {
	return file_observability_v0_sessions_proto_rawDescGZIP(), []int{2}
}

func (x *PartialSnapshot) GetUuid() string {
	if x != nil {
		return x.Uuid
	}
	return ""
}

func (x *PartialSnapshot) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *PartialSnapshot) GetProject() *ProjectSnapshot {
	if x != nil {
		return x.Project
	}
	return nil
}

func (x *PartialSnapshot) GetApplications() []*ApplicationSnapshot {
	if x != nil {
		return x.Applications
	}
	return nil
}

type Session struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Uuid string                 `protobuf:"bytes,1,opt,name=uuid,proto3" json:"uuid,omitempty"`
	At   *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=at,proto3" json:"at,omitempty"`
	// Types that are assignable to Session:
	//
	//	*Session_Partial
	//	*Session_Application
	Session isSession_Session `protobuf_oneof:"session"`
}

func (x *Session) Reset() {
	*x = Session{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_sessions_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Session) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Session) ProtoMessage() {}

func (x *Session) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_sessions_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Session.ProtoReflect.Descriptor instead.
func (*Session) Descriptor() ([]byte, []int) {
	return file_observability_v0_sessions_proto_rawDescGZIP(), []int{3}
}

func (x *Session) GetUuid() string {
	if x != nil {
		return x.Uuid
	}
	return ""
}

func (x *Session) GetAt() *timestamppb.Timestamp {
	if x != nil {
		return x.At
	}
	return nil
}

func (m *Session) GetSession() isSession_Session {
	if m != nil {
		return m.Session
	}
	return nil
}

func (x *Session) GetPartial() *PartialSnapshot {
	if x, ok := x.GetSession().(*Session_Partial); ok {
		return x.Partial
	}
	return nil
}

func (x *Session) GetApplication() *ApplicationSnapshot {
	if x, ok := x.GetSession().(*Session_Application); ok {
		return x.Application
	}
	return nil
}

type isSession_Session interface {
	isSession_Session()
}

type Session_Partial struct {
	Partial *PartialSnapshot `protobuf:"bytes,3,opt,name=partial,proto3,oneof"`
}

type Session_Application struct {
	Application *ApplicationSnapshot `protobuf:"bytes,4,opt,name=application,proto3,oneof"`
}

func (*Session_Partial) isSession_Session() {}

func (*Session_Application) isSession_Session() {}

var File_observability_v0_sessions_proto protoreflect.FileDescriptor

var file_observability_v0_sessions_proto_rawDesc = []byte{
	0x0a, 0x1f, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f,
	0x76, 0x30, 0x2f, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x10, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79,
	0x2e, 0x76, 0x30, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x22, 0x39, 0x0a, 0x0f, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x53,
	0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x75, 0x75, 0x69, 0x64, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x75, 0x75, 0x69, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x22,
	0x7a, 0x0a, 0x13, 0x41, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x53, 0x6e,
	0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x75, 0x75, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x75, 0x75, 0x69, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x3b,
	0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x21, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e,
	0x76, 0x30, 0x2e, 0x50, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x53, 0x6e, 0x61, 0x70, 0x73, 0x68,
	0x6f, 0x74, 0x52, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65, 0x63, 0x74, 0x22, 0xc1, 0x01, 0x0a, 0x0f,
	0x50, 0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x53, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x12,
	0x12, 0x0a, 0x04, 0x75, 0x75, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x75,
	0x75, 0x69, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x3b, 0x0a, 0x07, 0x70, 0x72, 0x6f, 0x6a, 0x65,
	0x63, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x21, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72,
	0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x50, 0x72, 0x6f, 0x6a,
	0x65, 0x63, 0x74, 0x53, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x52, 0x07, 0x70, 0x72, 0x6f,
	0x6a, 0x65, 0x63, 0x74, 0x12, 0x49, 0x0a, 0x0c, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x25, 0x2e, 0x6f, 0x62, 0x73,
	0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x70,
	0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x53, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f,
	0x74, 0x52, 0x0c, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x22,
	0xde, 0x01, 0x0a, 0x07, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x12, 0x12, 0x0a, 0x04, 0x75,
	0x75, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x75, 0x75, 0x69, 0x64, 0x12,
	0x2a, 0x0a, 0x02, 0x61, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x02, 0x61, 0x74, 0x12, 0x3d, 0x0a, 0x07, 0x70,
	0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x21, 0x2e, 0x6f,
	0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e,
	0x50, 0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x53, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x48,
	0x00, 0x52, 0x07, 0x70, 0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x12, 0x49, 0x0a, 0x0b, 0x61, 0x70,
	0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x25, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e,
	0x76, 0x30, 0x2e, 0x41, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x53, 0x6e,
	0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x48, 0x00, 0x52, 0x0b, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x42, 0x09, 0x0a, 0x07, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e,
	0x42, 0xc1, 0x01, 0x0a, 0x14, 0x63, 0x6f, 0x6d, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61,
	0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x42, 0x0d, 0x53, 0x65, 0x73, 0x73, 0x69,
	0x6f, 0x6e, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x39, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64,
	0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65,
	0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69,
	0x74, 0x79, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x4f, 0x56, 0x58, 0xaa, 0x02, 0x10, 0x4f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x56, 0x30, 0xca, 0x02,
	0x10, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x5c, 0x56,
	0x30, 0xe2, 0x02, 0x1c, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74,
	0x79, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61,
	0xea, 0x02, 0x11, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79,
	0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_observability_v0_sessions_proto_rawDescOnce sync.Once
	file_observability_v0_sessions_proto_rawDescData = file_observability_v0_sessions_proto_rawDesc
)

func file_observability_v0_sessions_proto_rawDescGZIP() []byte {
	file_observability_v0_sessions_proto_rawDescOnce.Do(func() {
		file_observability_v0_sessions_proto_rawDescData = protoimpl.X.CompressGZIP(file_observability_v0_sessions_proto_rawDescData)
	})
	return file_observability_v0_sessions_proto_rawDescData
}

var file_observability_v0_sessions_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_observability_v0_sessions_proto_goTypes = []interface{}{
	(*ProjectSnapshot)(nil),       // 0: observability.v0.ProjectSnapshot
	(*ApplicationSnapshot)(nil),   // 1: observability.v0.ApplicationSnapshot
	(*PartialSnapshot)(nil),       // 2: observability.v0.PartialSnapshot
	(*Session)(nil),               // 3: observability.v0.Session
	(*timestamppb.Timestamp)(nil), // 4: google.protobuf.Timestamp
}
var file_observability_v0_sessions_proto_depIdxs = []int32{
	0, // 0: observability.v0.ApplicationSnapshot.project:type_name -> observability.v0.ProjectSnapshot
	0, // 1: observability.v0.PartialSnapshot.project:type_name -> observability.v0.ProjectSnapshot
	1, // 2: observability.v0.PartialSnapshot.applications:type_name -> observability.v0.ApplicationSnapshot
	4, // 3: observability.v0.Session.at:type_name -> google.protobuf.Timestamp
	2, // 4: observability.v0.Session.partial:type_name -> observability.v0.PartialSnapshot
	1, // 5: observability.v0.Session.application:type_name -> observability.v0.ApplicationSnapshot
	6, // [6:6] is the sub-list for method output_type
	6, // [6:6] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_observability_v0_sessions_proto_init() }
func file_observability_v0_sessions_proto_init() {
	if File_observability_v0_sessions_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_observability_v0_sessions_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ProjectSnapshot); i {
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
		file_observability_v0_sessions_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApplicationSnapshot); i {
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
		file_observability_v0_sessions_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PartialSnapshot); i {
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
		file_observability_v0_sessions_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Session); i {
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
	file_observability_v0_sessions_proto_msgTypes[3].OneofWrappers = []interface{}{
		(*Session_Partial)(nil),
		(*Session_Application)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_observability_v0_sessions_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_observability_v0_sessions_proto_goTypes,
		DependencyIndexes: file_observability_v0_sessions_proto_depIdxs,
		MessageInfos:      file_observability_v0_sessions_proto_msgTypes,
	}.Build()
	File_observability_v0_sessions_proto = out.File
	file_observability_v0_sessions_proto_rawDesc = nil
	file_observability_v0_sessions_proto_goTypes = nil
	file_observability_v0_sessions_proto_depIdxs = nil
}
