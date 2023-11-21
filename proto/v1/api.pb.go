// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: api.proto

package v1

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

type HttpMethod int32

const (
	HttpMethod_GET     HttpMethod = 0
	HttpMethod_POST    HttpMethod = 1
	HttpMethod_PUT     HttpMethod = 2
	HttpMethod_DELETE  HttpMethod = 3
	HttpMethod_PATCH   HttpMethod = 4
	HttpMethod_OPTIONS HttpMethod = 5
	HttpMethod_HEAD    HttpMethod = 6
	HttpMethod_CONNECT HttpMethod = 7
	HttpMethod_TRACE   HttpMethod = 8
)

// Enum value maps for HttpMethod.
var (
	HttpMethod_name = map[int32]string{
		0: "GET",
		1: "POST",
		2: "PUT",
		3: "DELETE",
		4: "PATCH",
		5: "OPTIONS",
		6: "HEAD",
		7: "CONNECT",
		8: "TRACE",
	}
	HttpMethod_value = map[string]int32{
		"GET":     0,
		"POST":    1,
		"PUT":     2,
		"DELETE":  3,
		"PATCH":   4,
		"OPTIONS": 5,
		"HEAD":    6,
		"CONNECT": 7,
		"TRACE":   8,
	}
)

func (x HttpMethod) Enum() *HttpMethod {
	p := new(HttpMethod)
	*p = x
	return p
}

func (x HttpMethod) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (HttpMethod) Descriptor() protoreflect.EnumDescriptor {
	return file_api_proto_enumTypes[0].Descriptor()
}

func (HttpMethod) Type() protoreflect.EnumType {
	return &file_api_proto_enumTypes[0]
}

func (x HttpMethod) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use HttpMethod.Descriptor instead.
func (HttpMethod) EnumDescriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{0}
}

type Endpoint struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name        string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	Public      bool   `protobuf:"varint,3,opt,name=public,proto3" json:"public,omitempty"`
	Api         *API   `protobuf:"bytes,4,opt,name=api,proto3" json:"api,omitempty"`
}

func (x *Endpoint) Reset() {
	*x = Endpoint{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Endpoint) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Endpoint) ProtoMessage() {}

func (x *Endpoint) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Endpoint.ProtoReflect.Descriptor instead.
func (*Endpoint) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{0}
}

func (x *Endpoint) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Endpoint) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Endpoint) GetPublic() bool {
	if x != nil {
		return x.Public
	}
	return false
}

func (x *Endpoint) GetApi() *API {
	if x != nil {
		return x.Api
	}
	return nil
}

type ServiceEndpointGroup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Unique    string      `protobuf:"bytes,1,opt,name=unique,proto3" json:"unique,omitempty"`
	Public    bool        `protobuf:"varint,2,opt,name=public,proto3" json:"public,omitempty"`
	Endpoints []*Endpoint `protobuf:"bytes,3,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
}

func (x *ServiceEndpointGroup) Reset() {
	*x = ServiceEndpointGroup{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ServiceEndpointGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ServiceEndpointGroup) ProtoMessage() {}

func (x *ServiceEndpointGroup) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ServiceEndpointGroup.ProtoReflect.Descriptor instead.
func (*ServiceEndpointGroup) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{1}
}

func (x *ServiceEndpointGroup) GetUnique() string {
	if x != nil {
		return x.Unique
	}
	return ""
}

func (x *ServiceEndpointGroup) GetPublic() bool {
	if x != nil {
		return x.Public
	}
	return false
}

func (x *ServiceEndpointGroup) GetEndpoints() []*Endpoint {
	if x != nil {
		return x.Endpoints
	}
	return nil
}

type ApplicationEndpointGroup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name                  string                  `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Public                bool                    `protobuf:"varint,2,opt,name=public,proto3" json:"public,omitempty"`
	ServiceEndpointGroups []*ServiceEndpointGroup `protobuf:"bytes,3,rep,name=service_endpoint_groups,json=serviceEndpointGroups,proto3" json:"service_endpoint_groups,omitempty"`
}

func (x *ApplicationEndpointGroup) Reset() {
	*x = ApplicationEndpointGroup{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApplicationEndpointGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApplicationEndpointGroup) ProtoMessage() {}

func (x *ApplicationEndpointGroup) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApplicationEndpointGroup.ProtoReflect.Descriptor instead.
func (*ApplicationEndpointGroup) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{2}
}

func (x *ApplicationEndpointGroup) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ApplicationEndpointGroup) GetPublic() bool {
	if x != nil {
		return x.Public
	}
	return false
}

func (x *ApplicationEndpointGroup) GetServiceEndpointGroups() []*ServiceEndpointGroup {
	if x != nil {
		return x.ServiceEndpointGroups
	}
	return nil
}

type EndpointGroup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ApplicationEndpointGroup []*ApplicationEndpointGroup `protobuf:"bytes,2,rep,name=application_endpoint_group,json=applicationEndpointGroup,proto3" json:"application_endpoint_group,omitempty"`
}

func (x *EndpointGroup) Reset() {
	*x = EndpointGroup{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *EndpointGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EndpointGroup) ProtoMessage() {}

func (x *EndpointGroup) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EndpointGroup.ProtoReflect.Descriptor instead.
func (*EndpointGroup) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{3}
}

func (x *EndpointGroup) GetApplicationEndpointGroup() []*ApplicationEndpointGroup {
	if x != nil {
		return x.ApplicationEndpointGroup
	}
	return nil
}

type API struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Value:
	//
	//	*API_Tcp
	//	*API_Rest
	//	*API_Grpc
	Value isAPI_Value `protobuf_oneof:"value"`
}

func (x *API) Reset() {
	*x = API{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *API) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*API) ProtoMessage() {}

func (x *API) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use API.ProtoReflect.Descriptor instead.
func (*API) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{4}
}

func (m *API) GetValue() isAPI_Value {
	if m != nil {
		return m.Value
	}
	return nil
}

func (x *API) GetTcp() *TcpAPI {
	if x, ok := x.GetValue().(*API_Tcp); ok {
		return x.Tcp
	}
	return nil
}

func (x *API) GetRest() *RestAPI {
	if x, ok := x.GetValue().(*API_Rest); ok {
		return x.Rest
	}
	return nil
}

func (x *API) GetGrpc() *GrpcAPI {
	if x, ok := x.GetValue().(*API_Grpc); ok {
		return x.Grpc
	}
	return nil
}

type isAPI_Value interface {
	isAPI_Value()
}

type API_Tcp struct {
	Tcp *TcpAPI `protobuf:"bytes,1,opt,name=tcp,proto3,oneof"`
}

type API_Rest struct {
	Rest *RestAPI `protobuf:"bytes,2,opt,name=rest,proto3,oneof"`
}

type API_Grpc struct {
	Grpc *GrpcAPI `protobuf:"bytes,3,opt,name=grpc,proto3,oneof"`
}

func (*API_Tcp) isAPI_Value() {}

func (*API_Rest) isAPI_Value() {}

func (*API_Grpc) isAPI_Value() {}

type RestRoute struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Methods []HttpMethod `protobuf:"varint,1,rep,packed,name=methods,proto3,enum=v1.core.api.HttpMethod" json:"methods,omitempty"`
	Path    string       `protobuf:"bytes,2,opt,name=path,proto3" json:"path,omitempty"`
}

func (x *RestRoute) Reset() {
	*x = RestRoute{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RestRoute) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestRoute) ProtoMessage() {}

func (x *RestRoute) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestRoute.ProtoReflect.Descriptor instead.
func (*RestRoute) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{5}
}

func (x *RestRoute) GetMethods() []HttpMethod {
	if x != nil {
		return x.Methods
	}
	return nil
}

func (x *RestRoute) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

type RestAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Openapi []byte       `protobuf:"bytes,1,opt,name=openapi,proto3" json:"openapi,omitempty"`
	Routes  []*RestRoute `protobuf:"bytes,2,rep,name=routes,proto3" json:"routes,omitempty"`
}

func (x *RestAPI) Reset() {
	*x = RestAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RestAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestAPI) ProtoMessage() {}

func (x *RestAPI) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestAPI.ProtoReflect.Descriptor instead.
func (*RestAPI) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{6}
}

func (x *RestAPI) GetOpenapi() []byte {
	if x != nil {
		return x.Openapi
	}
	return nil
}

func (x *RestAPI) GetRoutes() []*RestRoute {
	if x != nil {
		return x.Routes
	}
	return nil
}

type GrpcAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Proto []byte `protobuf:"bytes,1,opt,name=proto,proto3" json:"proto,omitempty"`
}

func (x *GrpcAPI) Reset() {
	*x = GrpcAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcAPI) ProtoMessage() {}

func (x *GrpcAPI) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GrpcAPI.ProtoReflect.Descriptor instead.
func (*GrpcAPI) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{7}
}

func (x *GrpcAPI) GetProto() []byte {
	if x != nil {
		return x.Proto
	}
	return nil
}

type HttpAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *HttpAPI) Reset() {
	*x = HttpAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *HttpAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HttpAPI) ProtoMessage() {}

func (x *HttpAPI) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HttpAPI.ProtoReflect.Descriptor instead.
func (*HttpAPI) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{8}
}

type TcpAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TcpAPI) Reset() {
	*x = TcpAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_api_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TcpAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TcpAPI) ProtoMessage() {}

func (x *TcpAPI) ProtoReflect() protoreflect.Message {
	mi := &file_api_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TcpAPI.ProtoReflect.Descriptor instead.
func (*TcpAPI) Descriptor() ([]byte, []int) {
	return file_api_proto_rawDescGZIP(), []int{9}
}

var File_api_proto protoreflect.FileDescriptor

var file_api_proto_rawDesc = []byte{
	0x0a, 0x09, 0x61, 0x70, 0x69, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0b, 0x76, 0x31, 0x2e,
	0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x22, 0x7c, 0x0a, 0x08, 0x45, 0x6e, 0x64, 0x70,
	0x6f, 0x69, 0x6e, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63,
	0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64,
	0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x16, 0x0a, 0x06, 0x70, 0x75,
	0x62, 0x6c, 0x69, 0x63, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x70, 0x75, 0x62, 0x6c,
	0x69, 0x63, 0x12, 0x22, 0x0a, 0x03, 0x61, 0x70, 0x69, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x10, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x41, 0x50,
	0x49, 0x52, 0x03, 0x61, 0x70, 0x69, 0x22, 0x7b, 0x0a, 0x14, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x12, 0x16,
	0x0a, 0x06, 0x75, 0x6e, 0x69, 0x71, 0x75, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06,
	0x75, 0x6e, 0x69, 0x71, 0x75, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x70, 0x75, 0x62, 0x6c, 0x69, 0x63,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x70, 0x75, 0x62, 0x6c, 0x69, 0x63, 0x12, 0x33,
	0x0a, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x15, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e,
	0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x52, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69,
	0x6e, 0x74, 0x73, 0x22, 0xa1, 0x01, 0x0a, 0x18, 0x41, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x47, 0x72, 0x6f, 0x75, 0x70,
	0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x70, 0x75, 0x62, 0x6c, 0x69, 0x63, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x70, 0x75, 0x62, 0x6c, 0x69, 0x63, 0x12, 0x59, 0x0a, 0x17,
	0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74,
	0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x21, 0x2e,
	0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x53, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x47, 0x72, 0x6f, 0x75, 0x70,
	0x52, 0x15, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e,
	0x74, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x73, 0x22, 0x74, 0x0a, 0x0d, 0x45, 0x6e, 0x64, 0x70, 0x6f,
	0x69, 0x6e, 0x74, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x12, 0x63, 0x0a, 0x1a, 0x61, 0x70, 0x70, 0x6c,
	0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74,
	0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x25, 0x2e, 0x76,
	0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x41, 0x70, 0x70, 0x6c, 0x69,
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x47, 0x72,
	0x6f, 0x75, 0x70, 0x52, 0x18, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x22, 0x8f, 0x01,
	0x0a, 0x03, 0x41, 0x50, 0x49, 0x12, 0x27, 0x0a, 0x03, 0x74, 0x63, 0x70, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x13, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69,
	0x2e, 0x54, 0x63, 0x70, 0x41, 0x50, 0x49, 0x48, 0x00, 0x52, 0x03, 0x74, 0x63, 0x70, 0x12, 0x2a,
	0x0a, 0x04, 0x72, 0x65, 0x73, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x76,
	0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x52, 0x65, 0x73, 0x74, 0x41,
	0x50, 0x49, 0x48, 0x00, 0x52, 0x04, 0x72, 0x65, 0x73, 0x74, 0x12, 0x2a, 0x0a, 0x04, 0x67, 0x72,
	0x70, 0x63, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f,
	0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x47, 0x72, 0x70, 0x63, 0x41, 0x50, 0x49, 0x48, 0x00,
	0x52, 0x04, 0x67, 0x72, 0x70, 0x63, 0x42, 0x07, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x22,
	0x52, 0x0a, 0x09, 0x52, 0x65, 0x73, 0x74, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x12, 0x31, 0x0a, 0x07,
	0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0e, 0x32, 0x17, 0x2e,
	0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x48, 0x74, 0x74, 0x70,
	0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x52, 0x07, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x73, 0x12,
	0x12, 0x0a, 0x04, 0x70, 0x61, 0x74, 0x68, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x70,
	0x61, 0x74, 0x68, 0x22, 0x53, 0x0a, 0x07, 0x52, 0x65, 0x73, 0x74, 0x41, 0x50, 0x49, 0x12, 0x18,
	0x0a, 0x07, 0x6f, 0x70, 0x65, 0x6e, 0x61, 0x70, 0x69, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x07, 0x6f, 0x70, 0x65, 0x6e, 0x61, 0x70, 0x69, 0x12, 0x2e, 0x0a, 0x06, 0x72, 0x6f, 0x75, 0x74,
	0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f,
	0x72, 0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x52, 0x65, 0x73, 0x74, 0x52, 0x6f, 0x75, 0x74, 0x65,
	0x52, 0x06, 0x72, 0x6f, 0x75, 0x74, 0x65, 0x73, 0x22, 0x1f, 0x0a, 0x07, 0x47, 0x72, 0x70, 0x63,
	0x41, 0x50, 0x49, 0x12, 0x14, 0x0a, 0x05, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x05, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x09, 0x0a, 0x07, 0x48, 0x74, 0x74,
	0x70, 0x41, 0x50, 0x49, 0x22, 0x08, 0x0a, 0x06, 0x54, 0x63, 0x70, 0x41, 0x50, 0x49, 0x2a, 0x6e,
	0x0a, 0x0a, 0x48, 0x74, 0x74, 0x70, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x12, 0x07, 0x0a, 0x03,
	0x47, 0x45, 0x54, 0x10, 0x00, 0x12, 0x08, 0x0a, 0x04, 0x50, 0x4f, 0x53, 0x54, 0x10, 0x01, 0x12,
	0x07, 0x0a, 0x03, 0x50, 0x55, 0x54, 0x10, 0x02, 0x12, 0x0a, 0x0a, 0x06, 0x44, 0x45, 0x4c, 0x45,
	0x54, 0x45, 0x10, 0x03, 0x12, 0x09, 0x0a, 0x05, 0x50, 0x41, 0x54, 0x43, 0x48, 0x10, 0x04, 0x12,
	0x0b, 0x0a, 0x07, 0x4f, 0x50, 0x54, 0x49, 0x4f, 0x4e, 0x53, 0x10, 0x05, 0x12, 0x08, 0x0a, 0x04,
	0x48, 0x45, 0x41, 0x44, 0x10, 0x06, 0x12, 0x0b, 0x0a, 0x07, 0x43, 0x4f, 0x4e, 0x4e, 0x45, 0x43,
	0x54, 0x10, 0x07, 0x12, 0x09, 0x0a, 0x05, 0x54, 0x52, 0x41, 0x43, 0x45, 0x10, 0x08, 0x42, 0x89,
	0x01, 0x0a, 0x0f, 0x63, 0x6f, 0x6d, 0x2e, 0x76, 0x31, 0x2e, 0x63, 0x6f, 0x72, 0x65, 0x2e, 0x61,
	0x70, 0x69, 0x42, 0x08, 0x41, 0x70, 0x69, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x1e,
	0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x76, 0x31, 0xa2, 0x02,
	0x03, 0x56, 0x43, 0x41, 0xaa, 0x02, 0x0b, 0x56, 0x31, 0x2e, 0x43, 0x6f, 0x72, 0x65, 0x2e, 0x41,
	0x70, 0x69, 0xca, 0x02, 0x0b, 0x56, 0x31, 0x5c, 0x43, 0x6f, 0x72, 0x65, 0x5c, 0x41, 0x70, 0x69,
	0xe2, 0x02, 0x17, 0x56, 0x31, 0x5c, 0x43, 0x6f, 0x72, 0x65, 0x5c, 0x41, 0x70, 0x69, 0x5c, 0x47,
	0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0d, 0x56, 0x31, 0x3a,
	0x3a, 0x43, 0x6f, 0x72, 0x65, 0x3a, 0x3a, 0x41, 0x70, 0x69, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_api_proto_rawDescOnce sync.Once
	file_api_proto_rawDescData = file_api_proto_rawDesc
)

func file_api_proto_rawDescGZIP() []byte {
	file_api_proto_rawDescOnce.Do(func() {
		file_api_proto_rawDescData = protoimpl.X.CompressGZIP(file_api_proto_rawDescData)
	})
	return file_api_proto_rawDescData
}

var file_api_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_api_proto_msgTypes = make([]protoimpl.MessageInfo, 10)
var file_api_proto_goTypes = []interface{}{
	(HttpMethod)(0),                  // 0: v1.core.api.HttpMethod
	(*Endpoint)(nil),                 // 1: v1.core.api.Endpoint
	(*ServiceEndpointGroup)(nil),     // 2: v1.core.api.ServiceEndpointGroup
	(*ApplicationEndpointGroup)(nil), // 3: v1.core.api.ApplicationEndpointGroup
	(*EndpointGroup)(nil),            // 4: v1.core.api.EndpointGroup
	(*API)(nil),                      // 5: v1.core.api.API
	(*RestRoute)(nil),                // 6: v1.core.api.RestRoute
	(*RestAPI)(nil),                  // 7: v1.core.api.RestAPI
	(*GrpcAPI)(nil),                  // 8: v1.core.api.GrpcAPI
	(*HttpAPI)(nil),                  // 9: v1.core.api.HttpAPI
	(*TcpAPI)(nil),                   // 10: v1.core.api.TcpAPI
}
var file_api_proto_depIdxs = []int32{
	5,  // 0: v1.core.api.Endpoint.api:type_name -> v1.core.api.API
	1,  // 1: v1.core.api.ServiceEndpointGroup.endpoints:type_name -> v1.core.api.Endpoint
	2,  // 2: v1.core.api.ApplicationEndpointGroup.service_endpoint_groups:type_name -> v1.core.api.ServiceEndpointGroup
	3,  // 3: v1.core.api.EndpointGroup.application_endpoint_group:type_name -> v1.core.api.ApplicationEndpointGroup
	10, // 4: v1.core.api.API.tcp:type_name -> v1.core.api.TcpAPI
	7,  // 5: v1.core.api.API.rest:type_name -> v1.core.api.RestAPI
	8,  // 6: v1.core.api.API.grpc:type_name -> v1.core.api.GrpcAPI
	0,  // 7: v1.core.api.RestRoute.methods:type_name -> v1.core.api.HttpMethod
	6,  // 8: v1.core.api.RestAPI.routes:type_name -> v1.core.api.RestRoute
	9,  // [9:9] is the sub-list for method output_type
	9,  // [9:9] is the sub-list for method input_type
	9,  // [9:9] is the sub-list for extension type_name
	9,  // [9:9] is the sub-list for extension extendee
	0,  // [0:9] is the sub-list for field type_name
}

func init() { file_api_proto_init() }
func file_api_proto_init() {
	if File_api_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_api_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Endpoint); i {
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
		file_api_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ServiceEndpointGroup); i {
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
		file_api_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApplicationEndpointGroup); i {
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
		file_api_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*EndpointGroup); i {
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
		file_api_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*API); i {
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
		file_api_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RestRoute); i {
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
		file_api_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RestAPI); i {
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
		file_api_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GrpcAPI); i {
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
		file_api_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*HttpAPI); i {
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
		file_api_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TcpAPI); i {
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
	file_api_proto_msgTypes[4].OneofWrappers = []interface{}{
		(*API_Tcp)(nil),
		(*API_Rest)(nil),
		(*API_Grpc)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_api_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   10,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_api_proto_goTypes,
		DependencyIndexes: file_api_proto_depIdxs,
		EnumInfos:         file_api_proto_enumTypes,
		MessageInfos:      file_api_proto_msgTypes,
	}.Build()
	File_api_proto = out.File
	file_api_proto_rawDesc = nil
	file_api_proto_goTypes = nil
	file_api_proto_depIdxs = nil
}
