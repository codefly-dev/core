// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: proto/services/runtime/tracker.proto

package runtime

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

type ProcessTracker struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PID  int32  `protobuf:"varint,1,opt,name=PID,proto3" json:"PID,omitempty"`
	Type string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
}

func (x *ProcessTracker) Reset() {
	*x = ProcessTracker{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_services_runtime_tracker_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ProcessTracker) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ProcessTracker) ProtoMessage() {}

func (x *ProcessTracker) ProtoReflect() protoreflect.Message {
	mi := &file_proto_services_runtime_tracker_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ProcessTracker.ProtoReflect.Descriptor instead.
func (*ProcessTracker) Descriptor() ([]byte, []int) {
	return file_proto_services_runtime_tracker_proto_rawDescGZIP(), []int{0}
}

func (x *ProcessTracker) GetPID() int32 {
	if x != nil {
		return x.PID
	}
	return 0
}

func (x *ProcessTracker) GetType() string {
	if x != nil {
		return x.Type
	}
	return ""
}

type DockerTracker struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ContainerId string `protobuf:"bytes,1,opt,name=container_id,json=containerId,proto3" json:"container_id,omitempty"`
}

func (x *DockerTracker) Reset() {
	*x = DockerTracker{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_services_runtime_tracker_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DockerTracker) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DockerTracker) ProtoMessage() {}

func (x *DockerTracker) ProtoReflect() protoreflect.Message {
	mi := &file_proto_services_runtime_tracker_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DockerTracker.ProtoReflect.Descriptor instead.
func (*DockerTracker) Descriptor() ([]byte, []int) {
	return file_proto_services_runtime_tracker_proto_rawDescGZIP(), []int{1}
}

func (x *DockerTracker) GetContainerId() string {
	if x != nil {
		return x.ContainerId
	}
	return ""
}

type Tracker struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Types that are assignable to Tracker:
	//
	//	*Tracker_ProcessTracker
	//	*Tracker_DockerTracker
	Tracker isTracker_Tracker `protobuf_oneof:"tracker"`
}

func (x *Tracker) Reset() {
	*x = Tracker{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_services_runtime_tracker_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Tracker) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Tracker) ProtoMessage() {}

func (x *Tracker) ProtoReflect() protoreflect.Message {
	mi := &file_proto_services_runtime_tracker_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Tracker.ProtoReflect.Descriptor instead.
func (*Tracker) Descriptor() ([]byte, []int) {
	return file_proto_services_runtime_tracker_proto_rawDescGZIP(), []int{2}
}

func (x *Tracker) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (m *Tracker) GetTracker() isTracker_Tracker {
	if m != nil {
		return m.Tracker
	}
	return nil
}

func (x *Tracker) GetProcessTracker() *ProcessTracker {
	if x, ok := x.GetTracker().(*Tracker_ProcessTracker); ok {
		return x.ProcessTracker
	}
	return nil
}

func (x *Tracker) GetDockerTracker() *DockerTracker {
	if x, ok := x.GetTracker().(*Tracker_DockerTracker); ok {
		return x.DockerTracker
	}
	return nil
}

type isTracker_Tracker interface {
	isTracker_Tracker()
}

type Tracker_ProcessTracker struct {
	ProcessTracker *ProcessTracker `protobuf:"bytes,4,opt,name=process_tracker,json=processTracker,proto3,oneof"`
}

type Tracker_DockerTracker struct {
	DockerTracker *DockerTracker `protobuf:"bytes,5,opt,name=docker_tracker,json=dockerTracker,proto3,oneof"`
}

func (*Tracker_ProcessTracker) isTracker_Tracker() {}

func (*Tracker_DockerTracker) isTracker_Tracker() {}

type TrackerList struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Trackers []*Tracker `protobuf:"bytes,1,rep,name=trackers,proto3" json:"trackers,omitempty"`
}

func (x *TrackerList) Reset() {
	*x = TrackerList{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_services_runtime_tracker_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TrackerList) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TrackerList) ProtoMessage() {}

func (x *TrackerList) ProtoReflect() protoreflect.Message {
	mi := &file_proto_services_runtime_tracker_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TrackerList.ProtoReflect.Descriptor instead.
func (*TrackerList) Descriptor() ([]byte, []int) {
	return file_proto_services_runtime_tracker_proto_rawDescGZIP(), []int{3}
}

func (x *TrackerList) GetTrackers() []*Tracker {
	if x != nil {
		return x.Trackers
	}
	return nil
}

type Trackers struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Trackers map[string]*TrackerList `protobuf:"bytes,1,rep,name=trackers,proto3" json:"trackers,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (x *Trackers) Reset() {
	*x = Trackers{}
	if protoimpl.UnsafeEnabled {
		mi := &file_proto_services_runtime_tracker_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Trackers) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Trackers) ProtoMessage() {}

func (x *Trackers) ProtoReflect() protoreflect.Message {
	mi := &file_proto_services_runtime_tracker_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Trackers.ProtoReflect.Descriptor instead.
func (*Trackers) Descriptor() ([]byte, []int) {
	return file_proto_services_runtime_tracker_proto_rawDescGZIP(), []int{4}
}

func (x *Trackers) GetTrackers() map[string]*TrackerList {
	if x != nil {
		return x.Trackers
	}
	return nil
}

var File_proto_services_runtime_tracker_proto protoreflect.FileDescriptor

var file_proto_services_runtime_tracker_proto_rawDesc = []byte{
	0x0a, 0x24, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2f, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2f, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x13, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x73, 0x2e, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x22, 0x36, 0x0a, 0x0e, 0x50,
	0x72, 0x6f, 0x63, 0x65, 0x73, 0x73, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x12, 0x10, 0x0a,
	0x03, 0x50, 0x49, 0x44, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05, 0x52, 0x03, 0x50, 0x49, 0x44, 0x12,
	0x12, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x74,
	0x79, 0x70, 0x65, 0x22, 0x32, 0x0a, 0x0d, 0x44, 0x6f, 0x63, 0x6b, 0x65, 0x72, 0x54, 0x72, 0x61,
	0x63, 0x6b, 0x65, 0x72, 0x12, 0x21, 0x0a, 0x0c, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65,
	0x72, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x63, 0x6f, 0x6e, 0x74,
	0x61, 0x69, 0x6e, 0x65, 0x72, 0x49, 0x64, 0x22, 0xc5, 0x01, 0x0a, 0x07, 0x54, 0x72, 0x61, 0x63,
	0x6b, 0x65, 0x72, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x4e, 0x0a, 0x0f, 0x70, 0x72, 0x6f, 0x63, 0x65,
	0x73, 0x73, 0x5f, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x23, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x72,
	0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x50, 0x72, 0x6f, 0x63, 0x65, 0x73, 0x73, 0x54, 0x72,
	0x61, 0x63, 0x6b, 0x65, 0x72, 0x48, 0x00, 0x52, 0x0e, 0x70, 0x72, 0x6f, 0x63, 0x65, 0x73, 0x73,
	0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x12, 0x4b, 0x0a, 0x0e, 0x64, 0x6f, 0x63, 0x6b, 0x65,
	0x72, 0x5f, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x22, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x72, 0x75,
	0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x44, 0x6f, 0x63, 0x6b, 0x65, 0x72, 0x54, 0x72, 0x61, 0x63,
	0x6b, 0x65, 0x72, 0x48, 0x00, 0x52, 0x0d, 0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72, 0x54, 0x72, 0x61,
	0x63, 0x6b, 0x65, 0x72, 0x42, 0x09, 0x0a, 0x07, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x22,
	0x47, 0x0a, 0x0b, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x4c, 0x69, 0x73, 0x74, 0x12, 0x38,
	0x0a, 0x08, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x1c, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x72,
	0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x52, 0x08,
	0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x22, 0xb2, 0x01, 0x0a, 0x08, 0x54, 0x72, 0x61,
	0x63, 0x6b, 0x65, 0x72, 0x73, 0x12, 0x47, 0x0a, 0x08, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72,
	0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x2b, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x54, 0x72,
	0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x2e, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x45,
	0x6e, 0x74, 0x72, 0x79, 0x52, 0x08, 0x74, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x1a, 0x5d,
	0x0a, 0x0d, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12,
	0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65,
	0x79, 0x12, 0x36, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x20, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x72,
	0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x54, 0x72, 0x61, 0x63, 0x6b, 0x65, 0x72, 0x4c, 0x69,
	0x73, 0x74, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x42, 0xd9, 0x01,
	0x0a, 0x17, 0x63, 0x6f, 0x6d, 0x2e, 0x76, 0x31, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x2e, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x42, 0x0c, 0x54, 0x72, 0x61, 0x63, 0x6b,
	0x65, 0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x42, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65,
	0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64,
	0x2f, 0x76, 0x31, 0x2f, 0x67, 0x6f, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0xa2, 0x02, 0x03,
	0x56, 0x53, 0x52, 0xaa, 0x02, 0x13, 0x56, 0x31, 0x2e, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x2e, 0x52, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0xca, 0x02, 0x13, 0x56, 0x31, 0x5c, 0x53,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x52, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0xe2,
	0x02, 0x1f, 0x56, 0x31, 0x5c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x52, 0x75,
	0x6e, 0x74, 0x69, 0x6d, 0x65, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0xea, 0x02, 0x15, 0x56, 0x31, 0x3a, 0x3a, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x3a, 0x3a, 0x52, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_proto_services_runtime_tracker_proto_rawDescOnce sync.Once
	file_proto_services_runtime_tracker_proto_rawDescData = file_proto_services_runtime_tracker_proto_rawDesc
)

func file_proto_services_runtime_tracker_proto_rawDescGZIP() []byte {
	file_proto_services_runtime_tracker_proto_rawDescOnce.Do(func() {
		file_proto_services_runtime_tracker_proto_rawDescData = protoimpl.X.CompressGZIP(file_proto_services_runtime_tracker_proto_rawDescData)
	})
	return file_proto_services_runtime_tracker_proto_rawDescData
}

var file_proto_services_runtime_tracker_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_proto_services_runtime_tracker_proto_goTypes = []interface{}{
	(*ProcessTracker)(nil), // 0: v1.services.runtime.ProcessTracker
	(*DockerTracker)(nil),  // 1: v1.services.runtime.DockerTracker
	(*Tracker)(nil),        // 2: v1.services.runtime.Tracker
	(*TrackerList)(nil),    // 3: v1.services.runtime.TrackerList
	(*Trackers)(nil),       // 4: v1.services.runtime.Trackers
	nil,                    // 5: v1.services.runtime.Trackers.TrackersEntry
}
var file_proto_services_runtime_tracker_proto_depIdxs = []int32{
	0, // 0: v1.services.runtime.Tracker.process_tracker:type_name -> v1.services.runtime.ProcessTracker
	1, // 1: v1.services.runtime.Tracker.docker_tracker:type_name -> v1.services.runtime.DockerTracker
	2, // 2: v1.services.runtime.TrackerList.trackers:type_name -> v1.services.runtime.Tracker
	5, // 3: v1.services.runtime.Trackers.trackers:type_name -> v1.services.runtime.Trackers.TrackersEntry
	3, // 4: v1.services.runtime.Trackers.TrackersEntry.value:type_name -> v1.services.runtime.TrackerList
	5, // [5:5] is the sub-list for method output_type
	5, // [5:5] is the sub-list for method input_type
	5, // [5:5] is the sub-list for extension type_name
	5, // [5:5] is the sub-list for extension extendee
	0, // [0:5] is the sub-list for field type_name
}

func init() { file_proto_services_runtime_tracker_proto_init() }
func file_proto_services_runtime_tracker_proto_init() {
	if File_proto_services_runtime_tracker_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_proto_services_runtime_tracker_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ProcessTracker); i {
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
		file_proto_services_runtime_tracker_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DockerTracker); i {
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
		file_proto_services_runtime_tracker_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Tracker); i {
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
		file_proto_services_runtime_tracker_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TrackerList); i {
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
		file_proto_services_runtime_tracker_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Trackers); i {
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
	file_proto_services_runtime_tracker_proto_msgTypes[2].OneofWrappers = []interface{}{
		(*Tracker_ProcessTracker)(nil),
		(*Tracker_DockerTracker)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_proto_services_runtime_tracker_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_proto_services_runtime_tracker_proto_goTypes,
		DependencyIndexes: file_proto_services_runtime_tracker_proto_depIdxs,
		MessageInfos:      file_proto_services_runtime_tracker_proto_msgTypes,
	}.Build()
	File_proto_services_runtime_tracker_proto = out.File
	file_proto_services_runtime_tracker_proto_rawDesc = nil
	file_proto_services_runtime_tracker_proto_goTypes = nil
	file_proto_services_runtime_tracker_proto_depIdxs = nil
}