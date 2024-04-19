// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.0
// 	protoc        (unknown)
// source: observability/v0/dependencies.proto

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

type GraphNode_Type int32

const (
	GraphNode_MODULE   GraphNode_Type = 0
	GraphNode_SERVICE  GraphNode_Type = 1
	GraphNode_ENDPOINT GraphNode_Type = 2
)

// Enum value maps for GraphNode_Type.
var (
	GraphNode_Type_name = map[int32]string{
		0: "MODULE",
		1: "SERVICE",
		2: "ENDPOINT",
	}
	GraphNode_Type_value = map[string]int32{
		"MODULE":   0,
		"SERVICE":  1,
		"ENDPOINT": 2,
	}
)

func (x GraphNode_Type) Enum() *GraphNode_Type {
	p := new(GraphNode_Type)
	*p = x
	return p
}

func (x GraphNode_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GraphNode_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_observability_v0_dependencies_proto_enumTypes[0].Descriptor()
}

func (GraphNode_Type) Type() protoreflect.EnumType {
	return &file_observability_v0_dependencies_proto_enumTypes[0]
}

func (x GraphNode_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GraphNode_Type.Descriptor instead.
func (GraphNode_Type) EnumDescriptor() ([]byte, []int) {
	return file_observability_v0_dependencies_proto_rawDescGZIP(), []int{0, 0}
}

// GraphNode represents a node in an architecture graph
// Type can be "module", "service" or "endpoint"
type GraphNode struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id   string         `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Type GraphNode_Type `protobuf:"varint,2,opt,name=type,proto3,enum=observability.v0.GraphNode_Type" json:"type,omitempty"`
}

func (x *GraphNode) Reset() {
	*x = GraphNode{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_dependencies_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GraphNode) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GraphNode) ProtoMessage() {}

func (x *GraphNode) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_dependencies_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GraphNode.ProtoReflect.Descriptor instead.
func (*GraphNode) Descriptor() ([]byte, []int) {
	return file_observability_v0_dependencies_proto_rawDescGZIP(), []int{0}
}

func (x *GraphNode) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *GraphNode) GetType() GraphNode_Type {
	if x != nil {
		return x.Type
	}
	return GraphNode_MODULE
}

type GraphEdge struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	From string `protobuf:"bytes,1,opt,name=from,proto3" json:"from,omitempty"`
	To   string `protobuf:"bytes,2,opt,name=to,proto3" json:"to,omitempty"`
}

func (x *GraphEdge) Reset() {
	*x = GraphEdge{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_dependencies_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GraphEdge) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GraphEdge) ProtoMessage() {}

func (x *GraphEdge) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_dependencies_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GraphEdge.ProtoReflect.Descriptor instead.
func (*GraphEdge) Descriptor() ([]byte, []int) {
	return file_observability_v0_dependencies_proto_rawDescGZIP(), []int{1}
}

func (x *GraphEdge) GetFrom() string {
	if x != nil {
		return x.From
	}
	return ""
}

func (x *GraphEdge) GetTo() string {
	if x != nil {
		return x.To
	}
	return ""
}

type GraphResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Nodes []*GraphNode `protobuf:"bytes,1,rep,name=nodes,proto3" json:"nodes,omitempty"`
	Edges []*GraphEdge `protobuf:"bytes,2,rep,name=edges,proto3" json:"edges,omitempty"`
}

func (x *GraphResponse) Reset() {
	*x = GraphResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_observability_v0_dependencies_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GraphResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GraphResponse) ProtoMessage() {}

func (x *GraphResponse) ProtoReflect() protoreflect.Message {
	mi := &file_observability_v0_dependencies_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GraphResponse.ProtoReflect.Descriptor instead.
func (*GraphResponse) Descriptor() ([]byte, []int) {
	return file_observability_v0_dependencies_proto_rawDescGZIP(), []int{2}
}

func (x *GraphResponse) GetNodes() []*GraphNode {
	if x != nil {
		return x.Nodes
	}
	return nil
}

func (x *GraphResponse) GetEdges() []*GraphEdge {
	if x != nil {
		return x.Edges
	}
	return nil
}

var File_observability_v0_dependencies_proto protoreflect.FileDescriptor

var file_observability_v0_dependencies_proto_rawDesc = []byte{
	0x0a, 0x23, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f,
	0x76, 0x30, 0x2f, 0x64, 0x65, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x69, 0x65, 0x73, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x10, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69,
	0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x22, 0x80, 0x01, 0x0a, 0x09, 0x47, 0x72, 0x61, 0x70,
	0x68, 0x4e, 0x6f, 0x64, 0x65, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x02, 0x69, 0x64, 0x12, 0x34, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0e, 0x32, 0x20, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c,
	0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x47, 0x72, 0x61, 0x70, 0x68, 0x4e, 0x6f, 0x64, 0x65,
	0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x2d, 0x0a, 0x04, 0x54,
	0x79, 0x70, 0x65, 0x12, 0x0a, 0x0a, 0x06, 0x4d, 0x4f, 0x44, 0x55, 0x4c, 0x45, 0x10, 0x00, 0x12,
	0x0b, 0x0a, 0x07, 0x53, 0x45, 0x52, 0x56, 0x49, 0x43, 0x45, 0x10, 0x01, 0x12, 0x0c, 0x0a, 0x08,
	0x45, 0x4e, 0x44, 0x50, 0x4f, 0x49, 0x4e, 0x54, 0x10, 0x02, 0x22, 0x2f, 0x0a, 0x09, 0x47, 0x72,
	0x61, 0x70, 0x68, 0x45, 0x64, 0x67, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x66, 0x72, 0x6f, 0x6d, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x66, 0x72, 0x6f, 0x6d, 0x12, 0x0e, 0x0a, 0x02, 0x74,
	0x6f, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x74, 0x6f, 0x22, 0x75, 0x0a, 0x0d, 0x47,
	0x72, 0x61, 0x70, 0x68, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x31, 0x0a, 0x05,
	0x6e, 0x6f, 0x64, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1b, 0x2e, 0x6f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x2e, 0x47,
	0x72, 0x61, 0x70, 0x68, 0x4e, 0x6f, 0x64, 0x65, 0x52, 0x05, 0x6e, 0x6f, 0x64, 0x65, 0x73, 0x12,
	0x31, 0x0a, 0x05, 0x65, 0x64, 0x67, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1b,
	0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76,
	0x30, 0x2e, 0x47, 0x72, 0x61, 0x70, 0x68, 0x45, 0x64, 0x67, 0x65, 0x52, 0x05, 0x65, 0x64, 0x67,
	0x65, 0x73, 0x42, 0xc5, 0x01, 0x0a, 0x14, 0x63, 0x6f, 0x6d, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72,
	0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x76, 0x30, 0x42, 0x11, 0x44, 0x65, 0x70,
	0x65, 0x6e, 0x64, 0x65, 0x6e, 0x63, 0x69, 0x65, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01,
	0x5a, 0x39, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64,
	0x65, 0x66, 0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65,
	0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x6f, 0x62, 0x73, 0x65, 0x72,
	0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x4f, 0x56,
	0x58, 0xaa, 0x02, 0x10, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74,
	0x79, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x10, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69,
	0x6c, 0x69, 0x74, 0x79, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1c, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76,
	0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x5c, 0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65,
	0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x11, 0x4f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61,
	0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_observability_v0_dependencies_proto_rawDescOnce sync.Once
	file_observability_v0_dependencies_proto_rawDescData = file_observability_v0_dependencies_proto_rawDesc
)

func file_observability_v0_dependencies_proto_rawDescGZIP() []byte {
	file_observability_v0_dependencies_proto_rawDescOnce.Do(func() {
		file_observability_v0_dependencies_proto_rawDescData = protoimpl.X.CompressGZIP(file_observability_v0_dependencies_proto_rawDescData)
	})
	return file_observability_v0_dependencies_proto_rawDescData
}

var file_observability_v0_dependencies_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_observability_v0_dependencies_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_observability_v0_dependencies_proto_goTypes = []interface{}{
	(GraphNode_Type)(0),   // 0: observability.v0.GraphNode.Type
	(*GraphNode)(nil),     // 1: observability.v0.GraphNode
	(*GraphEdge)(nil),     // 2: observability.v0.GraphEdge
	(*GraphResponse)(nil), // 3: observability.v0.GraphResponse
}
var file_observability_v0_dependencies_proto_depIdxs = []int32{
	0, // 0: observability.v0.GraphNode.type:type_name -> observability.v0.GraphNode.Type
	1, // 1: observability.v0.GraphResponse.nodes:type_name -> observability.v0.GraphNode
	2, // 2: observability.v0.GraphResponse.edges:type_name -> observability.v0.GraphEdge
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_observability_v0_dependencies_proto_init() }
func file_observability_v0_dependencies_proto_init() {
	if File_observability_v0_dependencies_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_observability_v0_dependencies_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GraphNode); i {
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
		file_observability_v0_dependencies_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GraphEdge); i {
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
		file_observability_v0_dependencies_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GraphResponse); i {
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
			RawDescriptor: file_observability_v0_dependencies_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_observability_v0_dependencies_proto_goTypes,
		DependencyIndexes: file_observability_v0_dependencies_proto_depIdxs,
		EnumInfos:         file_observability_v0_dependencies_proto_enumTypes,
		MessageInfos:      file_observability_v0_dependencies_proto_msgTypes,
	}.Build()
	File_observability_v0_dependencies_proto = out.File
	file_observability_v0_dependencies_proto_rawDesc = nil
	file_observability_v0_dependencies_proto_goTypes = nil
	file_observability_v0_dependencies_proto_depIdxs = nil
}
