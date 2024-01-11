// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: services/runtime/v0/runtime.proto

package v0

import (
	context "context"

	v0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	Runtime_Load_FullMethodName        = "/services.runtime.v0.Runtime/Load"
	Runtime_Init_FullMethodName        = "/services.runtime.v0.Runtime/Init"
	Runtime_Start_FullMethodName       = "/services.runtime.v0.Runtime/Start"
	Runtime_Stop_FullMethodName        = "/services.runtime.v0.Runtime/Stop"
	Runtime_Information_FullMethodName = "/services.runtime.v0.Runtime/Information"
	Runtime_Communicate_FullMethodName = "/services.runtime.v0.Runtime/Communicate"
)

// RuntimeClient is the client API for Runtime service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RuntimeClient interface {
	// Load the Service Agent: this should be a NoOp and never fails
	Load(ctx context.Context, in *LoadRequest, opts ...grpc.CallOption) (*LoadResponse, error)
	// Init the Service Agent: could include steps like compilation, configuration, etc.
	// An important step of Initialization is to get the list of network mappings
	Init(ctx context.Context, in *InitRequest, opts ...grpc.CallOption) (*InitResponse, error)
	// Start the underlying service
	Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*StartResponse, error)
	// Stop the underlying service and cleanup
	Stop(ctx context.Context, in *StopRequest, opts ...grpc.CallOption) (*StopResponse, error)
	// Information about the state of the service
	Information(ctx context.Context, in *InformationRequest, opts ...grpc.CallOption) (*InformationResponse, error)
	// Communication helper
	Communicate(ctx context.Context, in *v0.Engage, opts ...grpc.CallOption) (*v0.InformationRequest, error)
}

type runtimeClient struct {
	cc grpc.ClientConnInterface
}

func NewRuntimeClient(cc grpc.ClientConnInterface) RuntimeClient {
	return &runtimeClient{cc}
}

func (c *runtimeClient) Load(ctx context.Context, in *LoadRequest, opts ...grpc.CallOption) (*LoadResponse, error) {
	out := new(LoadResponse)
	err := c.cc.Invoke(ctx, Runtime_Load_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Init(ctx context.Context, in *InitRequest, opts ...grpc.CallOption) (*InitResponse, error) {
	out := new(InitResponse)
	err := c.cc.Invoke(ctx, Runtime_Init_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*StartResponse, error) {
	out := new(StartResponse)
	err := c.cc.Invoke(ctx, Runtime_Start_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Stop(ctx context.Context, in *StopRequest, opts ...grpc.CallOption) (*StopResponse, error) {
	out := new(StopResponse)
	err := c.cc.Invoke(ctx, Runtime_Stop_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Information(ctx context.Context, in *InformationRequest, opts ...grpc.CallOption) (*InformationResponse, error) {
	out := new(InformationResponse)
	err := c.cc.Invoke(ctx, Runtime_Information_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Communicate(ctx context.Context, in *v0.Engage, opts ...grpc.CallOption) (*v0.InformationRequest, error) {
	out := new(v0.InformationRequest)
	err := c.cc.Invoke(ctx, Runtime_Communicate_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RuntimeServer is the server API for Runtime service.
// All implementations must embed UnimplementedRuntimeServer
// for forward compatibility
type RuntimeServer interface {
	// Load the Service Agent: this should be a NoOp and never fails
	Load(context.Context, *LoadRequest) (*LoadResponse, error)
	// Init the Service Agent: could include steps like compilation, configuration, etc.
	// An important step of Initialization is to get the list of network mappings
	Init(context.Context, *InitRequest) (*InitResponse, error)
	// Start the underlying service
	Start(context.Context, *StartRequest) (*StartResponse, error)
	// Stop the underlying service and cleanup
	Stop(context.Context, *StopRequest) (*StopResponse, error)
	// Information about the state of the service
	Information(context.Context, *InformationRequest) (*InformationResponse, error)
	// Communication helper
	Communicate(context.Context, *v0.Engage) (*v0.InformationRequest, error)
	mustEmbedUnimplementedRuntimeServer()
}

// UnimplementedRuntimeServer must be embedded to have forward compatible implementations.
type UnimplementedRuntimeServer struct {
}

func (UnimplementedRuntimeServer) Load(context.Context, *LoadRequest) (*LoadResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Load not implemented")
}
func (UnimplementedRuntimeServer) Init(context.Context, *InitRequest) (*InitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Init not implemented")
}
func (UnimplementedRuntimeServer) Start(context.Context, *StartRequest) (*StartResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Start not implemented")
}
func (UnimplementedRuntimeServer) Stop(context.Context, *StopRequest) (*StopResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Stop not implemented")
}
func (UnimplementedRuntimeServer) Information(context.Context, *InformationRequest) (*InformationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Information not implemented")
}
func (UnimplementedRuntimeServer) Communicate(context.Context, *v0.Engage) (*v0.InformationRequest, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Communicate not implemented")
}
func (UnimplementedRuntimeServer) mustEmbedUnimplementedRuntimeServer() {}

// UnsafeRuntimeServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to RuntimeServer will
// result in compilation errors.
type UnsafeRuntimeServer interface {
	mustEmbedUnimplementedRuntimeServer()
}

func RegisterRuntimeServer(s grpc.ServiceRegistrar, srv RuntimeServer) {
	s.RegisterService(&Runtime_ServiceDesc, srv)
}

func _Runtime_Load_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(LoadRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Load(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Load_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Load(ctx, req.(*LoadRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Init_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Init(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Init_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Init(ctx, req.(*InitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Start_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Start(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Start_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Start(ctx, req.(*StartRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Stop_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StopRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Stop(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Stop_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Stop(ctx, req.(*StopRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Information_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InformationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Information(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Information_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Information(ctx, req.(*InformationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Communicate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(v0.Engage)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Communicate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Communicate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Communicate(ctx, req.(*v0.Engage))
	}
	return interceptor(ctx, in, info, handler)
}

// Runtime_ServiceDesc is the grpc.ServiceDesc for Runtime service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Runtime_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "services.runtime.v0.Runtime",
	HandlerType: (*RuntimeServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Load",
			Handler:    _Runtime_Load_Handler,
		},
		{
			MethodName: "Init",
			Handler:    _Runtime_Init_Handler,
		},
		{
			MethodName: "Start",
			Handler:    _Runtime_Start_Handler,
		},
		{
			MethodName: "Stop",
			Handler:    _Runtime_Stop_Handler,
		},
		{
			MethodName: "Information",
			Handler:    _Runtime_Information_Handler,
		},
		{
			MethodName: "Communicate",
			Handler:    _Runtime_Communicate_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "services/runtime/v0/runtime.proto",
}
