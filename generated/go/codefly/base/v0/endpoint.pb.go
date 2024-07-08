// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: codefly/base/v0/endpoint.proto

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

type HTTPMethod int32

const (
	HTTPMethod_GET     HTTPMethod = 0
	HTTPMethod_POST    HTTPMethod = 1
	HTTPMethod_PUT     HTTPMethod = 2
	HTTPMethod_DELETE  HTTPMethod = 3
	HTTPMethod_PATCH   HTTPMethod = 4
	HTTPMethod_OPTIONS HTTPMethod = 5
	HTTPMethod_HEAD    HTTPMethod = 6
	HTTPMethod_CONNECT HTTPMethod = 7
	HTTPMethod_TRACE   HTTPMethod = 8
)

// Enum value maps for HTTPMethod.
var (
	HTTPMethod_name = map[int32]string{
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
	HTTPMethod_value = map[string]int32{
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

func (x HTTPMethod) Enum() *HTTPMethod {
	p := new(HTTPMethod)
	*p = x
	return p
}

func (x HTTPMethod) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (HTTPMethod) Descriptor() protoreflect.EnumDescriptor {
	return file_codefly_base_v0_endpoint_proto_enumTypes[0].Descriptor()
}

func (HTTPMethod) Type() protoreflect.EnumType {
	return &file_codefly_base_v0_endpoint_proto_enumTypes[0]
}

func (x HTTPMethod) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use HTTPMethod.Descriptor instead.
func (HTTPMethod) EnumDescriptor() ([]byte, []int) {
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{0}
}

type Endpoint struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name of the endpoint: in lots of cases, the name of the endpoint will be the API name (http, grpc, tcp, etc...)
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Service name
	Service string `protobuf:"bytes,2,opt,name=service,proto3" json:"service,omitempty"`
	// Module name
	Module string `protobuf:"bytes,3,opt,name=module,proto3" json:"module,omitempty"`
	// Description of the endpoint
	Description string `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
	// Visibility of the endpoint
	Visibility string `protobuf:"bytes,5,opt,name=visibility,proto3" json:"visibility,omitempty"`
	Api        string `protobuf:"bytes,6,opt,name=api,proto3" json:"api,omitempty"`
	ApiDetails *API   `protobuf:"bytes,7,opt,name=api_details,json=apiDetails,proto3" json:"api_details,omitempty"`
}

func (x *Endpoint) Reset() {
	*x = Endpoint{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Endpoint) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Endpoint) ProtoMessage() {}

func (x *Endpoint) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[0]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{0}
}

func (x *Endpoint) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Endpoint) GetService() string {
	if x != nil {
		return x.Service
	}
	return ""
}

func (x *Endpoint) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *Endpoint) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *Endpoint) GetVisibility() string {
	if x != nil {
		return x.Visibility
	}
	return ""
}

func (x *Endpoint) GetApi() string {
	if x != nil {
		return x.Api
	}
	return ""
}

func (x *Endpoint) GetApiDetails() *API {
	if x != nil {
		return x.ApiDetails
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
	//	*API_Http
	//	*API_Rest
	//	*API_Grpc
	Value isAPI_Value `protobuf_oneof:"value"`
}

func (x *API) Reset() {
	*x = API{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *API) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*API) ProtoMessage() {}

func (x *API) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetAPI.ProtoReflect.Descriptor instead.
func (*API) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{1}
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

func (x *API) GetHttp() *HttpAPI {
	if x, ok := x.GetValue().(*API_Http); ok {
		return x.Http
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

type API_Http struct {
	Http *HttpAPI `protobuf:"bytes,2,opt,name=http,proto3,oneof"`
}

type API_Rest struct {
	Rest *RestAPI `protobuf:"bytes,3,opt,name=rest,proto3,oneof"`
}

type API_Grpc struct {
	Grpc *GrpcAPI `protobuf:"bytes,4,opt,name=grpc,proto3,oneof"`
}

func (*API_Tcp) isAPI_Value() {}

func (*API_Http) isAPI_Value() {}

func (*API_Rest) isAPI_Value() {}

func (*API_Grpc) isAPI_Value() {}

// A RestRouteGroup is a collection of routes that share the same path
type RestRouteGroup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	// TODO [(buf.validate.field).string.pattern = "^/([a-z0-9-/]+)*$", (buf.validate.field).string.min_len = 3, (buf.validate.field).string.max_len = 25];
	Routes []*RestRoute `protobuf:"bytes,2,rep,name=routes,proto3" json:"routes,omitempty"`
}

func (x *RestRouteGroup) Reset() {
	*x = RestRouteGroup{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RestRouteGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestRouteGroup) ProtoMessage() {}

func (x *RestRouteGroup) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestRouteGroup.ProtoReflect.Descriptor instead.
func (*RestRouteGroup) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{2}
}

func (x *RestRouteGroup) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

func (x *RestRouteGroup) GetRoutes() []*RestRoute {
	if x != nil {
		return x.Routes
	}
	return nil
}

// RestRoute represents the data of the route itself
// It is usually found through a Route group
type RestRoute struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	// TODO [(buf.validate.field).string.pattern = "^/([a-z0-9-/]+)*$", (buf.validate.field).string.min_len = 3, (buf.validate.field).string.max_len = 25];
	Method HTTPMethod `protobuf:"varint,2,opt,name=method,proto3,enum=codefly.base.v0.HTTPMethod" json:"method,omitempty"`
}

func (x *RestRoute) Reset() {
	*x = RestRoute{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RestRoute) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestRoute) ProtoMessage() {}

func (x *RestRoute) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[3]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{3}
}

func (x *RestRoute) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

func (x *RestRoute) GetMethod() HTTPMethod {
	if x != nil {
		return x.Method
	}
	return HTTPMethod_GET
}

type RestAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Service string            `protobuf:"bytes,1,opt,name=service,proto3" json:"service,omitempty"`
	Module  string            `protobuf:"bytes,2,opt,name=module,proto3" json:"module,omitempty"`
	Groups  []*RestRouteGroup `protobuf:"bytes,3,rep,name=groups,proto3" json:"groups,omitempty"`
	Openapi []byte            `protobuf:"bytes,4,opt,name=openapi,proto3" json:"openapi,omitempty"`
	Secured bool              `protobuf:"varint,5,opt,name=secured,proto3" json:"secured,omitempty"`
}

func (x *RestAPI) Reset() {
	*x = RestAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RestAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestAPI) ProtoMessage() {}

func (x *RestAPI) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[4]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{4}
}

func (x *RestAPI) GetService() string {
	if x != nil {
		return x.Service
	}
	return ""
}

func (x *RestAPI) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *RestAPI) GetGroups() []*RestRouteGroup {
	if x != nil {
		return x.Groups
	}
	return nil
}

func (x *RestAPI) GetOpenapi() []byte {
	if x != nil {
		return x.Openapi
	}
	return nil
}

func (x *RestAPI) GetSecured() bool {
	if x != nil {
		return x.Secured
	}
	return false
}

type RPC struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ServiceName string `protobuf:"bytes,1,opt,name=service_name,json=serviceName,proto3" json:"service_name,omitempty"`
	Name        string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

func (x *RPC) Reset() {
	*x = RPC{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RPC) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RPC) ProtoMessage() {}

func (x *RPC) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RPC.ProtoReflect.Descriptor instead.
func (*RPC) Descriptor() ([]byte, []int) {
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{5}
}

func (x *RPC) GetServiceName() string {
	if x != nil {
		return x.ServiceName
	}
	return ""
}

func (x *RPC) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type GrpcAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Service string `protobuf:"bytes,1,opt,name=service,proto3" json:"service,omitempty"`
	Module  string `protobuf:"bytes,2,opt,name=module,proto3" json:"module,omitempty"`
	Package string `protobuf:"bytes,3,opt,name=package,proto3" json:"package,omitempty"`
	Rpcs    []*RPC `protobuf:"bytes,4,rep,name=rpcs,proto3" json:"rpcs,omitempty"`
	Proto   []byte `protobuf:"bytes,5,opt,name=proto,proto3" json:"proto,omitempty"`
	Secured bool   `protobuf:"varint,6,opt,name=secured,proto3" json:"secured,omitempty"`
}

func (x *GrpcAPI) Reset() {
	*x = GrpcAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcAPI) ProtoMessage() {}

func (x *GrpcAPI) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[6]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{6}
}

func (x *GrpcAPI) GetService() string {
	if x != nil {
		return x.Service
	}
	return ""
}

func (x *GrpcAPI) GetModule() string {
	if x != nil {
		return x.Module
	}
	return ""
}

func (x *GrpcAPI) GetPackage() string {
	if x != nil {
		return x.Package
	}
	return ""
}

func (x *GrpcAPI) GetRpcs() []*RPC {
	if x != nil {
		return x.Rpcs
	}
	return nil
}

func (x *GrpcAPI) GetProto() []byte {
	if x != nil {
		return x.Proto
	}
	return nil
}

func (x *GrpcAPI) GetSecured() bool {
	if x != nil {
		return x.Secured
	}
	return false
}

type HttpAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Secured bool `protobuf:"varint,1,opt,name=secured,proto3" json:"secured,omitempty"`
}

func (x *HttpAPI) Reset() {
	*x = HttpAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *HttpAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HttpAPI) ProtoMessage() {}

func (x *HttpAPI) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[7]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{7}
}

func (x *HttpAPI) GetSecured() bool {
	if x != nil {
		return x.Secured
	}
	return false
}

type TcpAPI struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TcpAPI) Reset() {
	*x = TcpAPI{}
	if protoimpl.UnsafeEnabled {
		mi := &file_codefly_base_v0_endpoint_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TcpAPI) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TcpAPI) ProtoMessage() {}

func (x *TcpAPI) ProtoReflect() protoreflect.Message {
	mi := &file_codefly_base_v0_endpoint_proto_msgTypes[8]
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
	return file_codefly_base_v0_endpoint_proto_rawDescGZIP(), []int{8}
}

var File_codefly_base_v0_endpoint_proto protoreflect.FileDescriptor

var file_codefly_base_v0_endpoint_proto_rawDesc = []byte{
	0x0a, 0x1e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62, 0x61, 0x73, 0x65, 0x2f, 0x76,
	0x30, 0x2f, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x12, 0x0f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76,
	0x30, 0x1a, 0x1b, 0x62, 0x75, 0x66, 0x2f, 0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2f,
	0x76, 0x61, 0x6c, 0x69, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xfa,
	0x02, 0x0a, 0x08, 0x45, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e, 0x74, 0x12, 0x29, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x42, 0x15, 0xba, 0x48, 0x12, 0x72, 0x10,
	0x10, 0x03, 0x18, 0x14, 0x32, 0x08, 0x5e, 0x5b, 0x61, 0x2d, 0x7a, 0x5d, 0x2b, 0x24, 0x68, 0x01,
	0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x38, 0x0a, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x42, 0x1e, 0xba, 0x48, 0x1b, 0x72, 0x19, 0x10, 0x03,
	0x18, 0x19, 0x32, 0x0c, 0x5e, 0x5b, 0x61, 0x2d, 0x7a, 0x30, 0x2d, 0x39, 0x2d, 0x5d, 0x2b, 0x24,
	0xba, 0x01, 0x02, 0x2d, 0x2d, 0x68, 0x01, 0x52, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x12, 0x36, 0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09,
	0x42, 0x1e, 0xba, 0x48, 0x1b, 0x72, 0x19, 0x10, 0x03, 0x18, 0x19, 0x32, 0x0c, 0x5e, 0x5b, 0x61,
	0x2d, 0x7a, 0x30, 0x2d, 0x39, 0x2d, 0x5d, 0x2b, 0x24, 0xba, 0x01, 0x02, 0x2d, 0x2d, 0x68, 0x01,
	0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x20, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x63,
	0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x64,
	0x65, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x48, 0x0a, 0x0a, 0x76, 0x69,
	0x73, 0x69, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x42, 0x28,
	0xba, 0x48, 0x25, 0x72, 0x23, 0x52, 0x08, 0x65, 0x78, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x52,
	0x06, 0x70, 0x75, 0x62, 0x6c, 0x69, 0x63, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x52,
	0x07, 0x70, 0x72, 0x69, 0x76, 0x61, 0x74, 0x65, 0x52, 0x0a, 0x76, 0x69, 0x73, 0x69, 0x62, 0x69,
	0x6c, 0x69, 0x74, 0x79, 0x12, 0x2e, 0x0a, 0x03, 0x61, 0x70, 0x69, 0x18, 0x06, 0x20, 0x01, 0x28,
	0x09, 0x42, 0x1c, 0xba, 0x48, 0x19, 0x72, 0x17, 0x52, 0x04, 0x68, 0x74, 0x74, 0x70, 0x52, 0x04,
	0x67, 0x72, 0x70, 0x63, 0x52, 0x03, 0x74, 0x63, 0x70, 0x52, 0x04, 0x72, 0x65, 0x73, 0x74, 0x52,
	0x03, 0x61, 0x70, 0x69, 0x12, 0x35, 0x0a, 0x0b, 0x61, 0x70, 0x69, 0x5f, 0x64, 0x65, 0x74, 0x61,
	0x69, 0x6c, 0x73, 0x18, 0x07, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x41, 0x50, 0x49, 0x52,
	0x0a, 0x61, 0x70, 0x69, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x22, 0xcb, 0x01, 0x0a, 0x03,
	0x41, 0x50, 0x49, 0x12, 0x2b, 0x0a, 0x03, 0x74, 0x63, 0x70, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x17, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e,
	0x76, 0x30, 0x2e, 0x54, 0x63, 0x70, 0x41, 0x50, 0x49, 0x48, 0x00, 0x52, 0x03, 0x74, 0x63, 0x70,
	0x12, 0x2e, 0x0a, 0x04, 0x68, 0x74, 0x74, 0x70, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x18,
	0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x2e, 0x48, 0x74, 0x74, 0x70, 0x41, 0x50, 0x49, 0x48, 0x00, 0x52, 0x04, 0x68, 0x74, 0x74, 0x70,
	0x12, 0x2e, 0x0a, 0x04, 0x72, 0x65, 0x73, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x18,
	0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x2e, 0x52, 0x65, 0x73, 0x74, 0x41, 0x50, 0x49, 0x48, 0x00, 0x52, 0x04, 0x72, 0x65, 0x73, 0x74,
	0x12, 0x2e, 0x0a, 0x04, 0x67, 0x72, 0x70, 0x63, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x18,
	0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30,
	0x2e, 0x47, 0x72, 0x70, 0x63, 0x41, 0x50, 0x49, 0x48, 0x00, 0x52, 0x04, 0x67, 0x72, 0x70, 0x63,
	0x42, 0x07, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x22, 0x58, 0x0a, 0x0e, 0x52, 0x65, 0x73,
	0x74, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x12, 0x12, 0x0a, 0x04, 0x70,
	0x61, 0x74, 0x68, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x70, 0x61, 0x74, 0x68, 0x12,
	0x32, 0x0a, 0x06, 0x72, 0x6f, 0x75, 0x74, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32,
	0x1a, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76,
	0x30, 0x2e, 0x52, 0x65, 0x73, 0x74, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x52, 0x06, 0x72, 0x6f, 0x75,
	0x74, 0x65, 0x73, 0x22, 0x54, 0x0a, 0x09, 0x52, 0x65, 0x73, 0x74, 0x52, 0x6f, 0x75, 0x74, 0x65,
	0x12, 0x12, 0x0a, 0x04, 0x70, 0x61, 0x74, 0x68, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04,
	0x70, 0x61, 0x74, 0x68, 0x12, 0x33, 0x0a, 0x06, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x0e, 0x32, 0x1b, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x62,
	0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x48, 0x54, 0x54, 0x50, 0x4d, 0x65, 0x74, 0x68, 0x6f,
	0x64, 0x52, 0x06, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x22, 0xa8, 0x01, 0x0a, 0x07, 0x52, 0x65,
	0x73, 0x74, 0x41, 0x50, 0x49, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12,
	0x16, 0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x37, 0x0a, 0x06, 0x67, 0x72, 0x6f, 0x75, 0x70,
	0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1f, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c,
	0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x52, 0x65, 0x73, 0x74, 0x52, 0x6f,
	0x75, 0x74, 0x65, 0x47, 0x72, 0x6f, 0x75, 0x70, 0x52, 0x06, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x73,
	0x12, 0x18, 0x0a, 0x07, 0x6f, 0x70, 0x65, 0x6e, 0x61, 0x70, 0x69, 0x18, 0x04, 0x20, 0x01, 0x28,
	0x0c, 0x52, 0x07, 0x6f, 0x70, 0x65, 0x6e, 0x61, 0x70, 0x69, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65,
	0x63, 0x75, 0x72, 0x65, 0x64, 0x18, 0x05, 0x20, 0x01, 0x28, 0x08, 0x52, 0x07, 0x73, 0x65, 0x63,
	0x75, 0x72, 0x65, 0x64, 0x22, 0x3c, 0x0a, 0x03, 0x52, 0x50, 0x43, 0x12, 0x21, 0x0a, 0x0c, 0x73,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x0b, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x12,
	0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x22, 0xaf, 0x01, 0x0a, 0x07, 0x47, 0x72, 0x70, 0x63, 0x41, 0x50, 0x49, 0x12, 0x18,
	0x0a, 0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x07, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75,
	0x6c, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65,
	0x12, 0x18, 0x0a, 0x07, 0x70, 0x61, 0x63, 0x6b, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x70, 0x61, 0x63, 0x6b, 0x61, 0x67, 0x65, 0x12, 0x28, 0x0a, 0x04, 0x72, 0x70,
	0x63, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x64, 0x65, 0x66,
	0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x2e, 0x52, 0x50, 0x43, 0x52, 0x04,
	0x72, 0x70, 0x63, 0x73, 0x12, 0x14, 0x0a, 0x05, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x18, 0x05, 0x20,
	0x01, 0x28, 0x0c, 0x52, 0x05, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65,
	0x63, 0x75, 0x72, 0x65, 0x64, 0x18, 0x06, 0x20, 0x01, 0x28, 0x08, 0x52, 0x07, 0x73, 0x65, 0x63,
	0x75, 0x72, 0x65, 0x64, 0x22, 0x23, 0x0a, 0x07, 0x48, 0x74, 0x74, 0x70, 0x41, 0x50, 0x49, 0x12,
	0x18, 0x0a, 0x07, 0x73, 0x65, 0x63, 0x75, 0x72, 0x65, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x08,
	0x52, 0x07, 0x73, 0x65, 0x63, 0x75, 0x72, 0x65, 0x64, 0x22, 0x08, 0x0a, 0x06, 0x54, 0x63, 0x70,
	0x41, 0x50, 0x49, 0x2a, 0x6e, 0x0a, 0x0a, 0x48, 0x54, 0x54, 0x50, 0x4d, 0x65, 0x74, 0x68, 0x6f,
	0x64, 0x12, 0x07, 0x0a, 0x03, 0x47, 0x45, 0x54, 0x10, 0x00, 0x12, 0x08, 0x0a, 0x04, 0x50, 0x4f,
	0x53, 0x54, 0x10, 0x01, 0x12, 0x07, 0x0a, 0x03, 0x50, 0x55, 0x54, 0x10, 0x02, 0x12, 0x0a, 0x0a,
	0x06, 0x44, 0x45, 0x4c, 0x45, 0x54, 0x45, 0x10, 0x03, 0x12, 0x09, 0x0a, 0x05, 0x50, 0x41, 0x54,
	0x43, 0x48, 0x10, 0x04, 0x12, 0x0b, 0x0a, 0x07, 0x4f, 0x50, 0x54, 0x49, 0x4f, 0x4e, 0x53, 0x10,
	0x05, 0x12, 0x08, 0x0a, 0x04, 0x48, 0x45, 0x41, 0x44, 0x10, 0x06, 0x12, 0x0b, 0x0a, 0x07, 0x43,
	0x4f, 0x4e, 0x4e, 0x45, 0x43, 0x54, 0x10, 0x07, 0x12, 0x09, 0x0a, 0x05, 0x54, 0x52, 0x41, 0x43,
	0x45, 0x10, 0x08, 0x42, 0xbc, 0x01, 0x0a, 0x13, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x6f, 0x64, 0x65,
	0x66, 0x6c, 0x79, 0x2e, 0x62, 0x61, 0x73, 0x65, 0x2e, 0x76, 0x30, 0x42, 0x0d, 0x45, 0x6e, 0x64,
	0x70, 0x6f, 0x69, 0x6e, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x38, 0x67, 0x69,
	0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79,
	0x2d, 0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61,
	0x74, 0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2f, 0x62,
	0x61, 0x73, 0x65, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x43, 0x42, 0x56, 0xaa, 0x02, 0x0f, 0x43,
	0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2e, 0x42, 0x61, 0x73, 0x65, 0x2e, 0x56, 0x30, 0xca, 0x02,
	0x0f, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c, 0x56, 0x30,
	0xe2, 0x02, 0x1b, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x5c, 0x42, 0x61, 0x73, 0x65, 0x5c,
	0x56, 0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02,
	0x11, 0x43, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x3a, 0x3a, 0x42, 0x61, 0x73, 0x65, 0x3a, 0x3a,
	0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_codefly_base_v0_endpoint_proto_rawDescOnce sync.Once
	file_codefly_base_v0_endpoint_proto_rawDescData = file_codefly_base_v0_endpoint_proto_rawDesc
)

func file_codefly_base_v0_endpoint_proto_rawDescGZIP() []byte {
	file_codefly_base_v0_endpoint_proto_rawDescOnce.Do(func() {
		file_codefly_base_v0_endpoint_proto_rawDescData = protoimpl.X.CompressGZIP(file_codefly_base_v0_endpoint_proto_rawDescData)
	})
	return file_codefly_base_v0_endpoint_proto_rawDescData
}

var file_codefly_base_v0_endpoint_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_codefly_base_v0_endpoint_proto_msgTypes = make([]protoimpl.MessageInfo, 9)
var file_codefly_base_v0_endpoint_proto_goTypes = []any{
	(HTTPMethod)(0),        // 0: codefly.base.v0.HTTPMethod
	(*Endpoint)(nil),       // 1: codefly.base.v0.Endpoint
	(*API)(nil),            // 2: codefly.base.v0.GetAPI
	(*RestRouteGroup)(nil), // 3: codefly.base.v0.RestRouteGroup
	(*RestRoute)(nil),      // 4: codefly.base.v0.RestRoute
	(*RestAPI)(nil),        // 5: codefly.base.v0.RestAPI
	(*RPC)(nil),            // 6: codefly.base.v0.RPC
	(*GrpcAPI)(nil),        // 7: codefly.base.v0.GrpcAPI
	(*HttpAPI)(nil),        // 8: codefly.base.v0.HttpAPI
	(*TcpAPI)(nil),         // 9: codefly.base.v0.TcpAPI
}
var file_codefly_base_v0_endpoint_proto_depIdxs = []int32{
	2, // 0: codefly.base.v0.Endpoint.api_details:type_name -> codefly.base.v0.GetAPI
	9, // 1: codefly.base.v0.GetAPI.tcp:type_name -> codefly.base.v0.TcpAPI
	8, // 2: codefly.base.v0.GetAPI.http:type_name -> codefly.base.v0.HttpAPI
	5, // 3: codefly.base.v0.GetAPI.rest:type_name -> codefly.base.v0.RestAPI
	7, // 4: codefly.base.v0.GetAPI.grpc:type_name -> codefly.base.v0.GrpcAPI
	4, // 5: codefly.base.v0.RestRouteGroup.routes:type_name -> codefly.base.v0.RestRoute
	0, // 6: codefly.base.v0.RestRoute.method:type_name -> codefly.base.v0.HTTPMethod
	3, // 7: codefly.base.v0.RestAPI.groups:type_name -> codefly.base.v0.RestRouteGroup
	6, // 8: codefly.base.v0.GrpcAPI.rpcs:type_name -> codefly.base.v0.RPC
	9, // [9:9] is the sub-list for method output_type
	9, // [9:9] is the sub-list for method input_type
	9, // [9:9] is the sub-list for extension type_name
	9, // [9:9] is the sub-list for extension extendee
	0, // [0:9] is the sub-list for field type_name
}

func init() { file_codefly_base_v0_endpoint_proto_init() }
func file_codefly_base_v0_endpoint_proto_init() {
	if File_codefly_base_v0_endpoint_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_codefly_base_v0_endpoint_proto_msgTypes[0].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[1].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*RestRouteGroup); i {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[3].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[4].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[5].Exporter = func(v any, i int) any {
			switch v := v.(*RPC); i {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[6].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[7].Exporter = func(v any, i int) any {
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
		file_codefly_base_v0_endpoint_proto_msgTypes[8].Exporter = func(v any, i int) any {
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
	file_codefly_base_v0_endpoint_proto_msgTypes[1].OneofWrappers = []any{
		(*API_Tcp)(nil),
		(*API_Http)(nil),
		(*API_Rest)(nil),
		(*API_Grpc)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_codefly_base_v0_endpoint_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   9,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_codefly_base_v0_endpoint_proto_goTypes,
		DependencyIndexes: file_codefly_base_v0_endpoint_proto_depIdxs,
		EnumInfos:         file_codefly_base_v0_endpoint_proto_enumTypes,
		MessageInfos:      file_codefly_base_v0_endpoint_proto_msgTypes,
	}.Build()
	File_codefly_base_v0_endpoint_proto = out.File
	file_codefly_base_v0_endpoint_proto_rawDesc = nil
	file_codefly_base_v0_endpoint_proto_goTypes = nil
	file_codefly_base_v0_endpoint_proto_depIdxs = nil
}
