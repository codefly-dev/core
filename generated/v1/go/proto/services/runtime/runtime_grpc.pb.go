// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: proto/services/runtime/runtime.proto

package runtime

import (
	context "context"
	agents "github.com/codefly-dev/core/generated/v1/go/proto/agents"
	services "github.com/codefly-dev/core/generated/v1/go/proto/services"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	Runtime_Init_FullMethodName        = "/v1.services.runtime.Runtime/Init"
	Runtime_Configure_FullMethodName   = "/v1.services.runtime.Runtime/Configure"
	Runtime_Start_FullMethodName       = "/v1.services.runtime.Runtime/Start"
	Runtime_Information_FullMethodName = "/v1.services.runtime.Runtime/Information"
	Runtime_Stop_FullMethodName        = "/v1.services.runtime.Runtime/Stop"
	Runtime_Communicate_FullMethodName = "/v1.services.runtime.Runtime/Communicate"
)

// RuntimeClient is the client API for Runtime service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RuntimeClient interface {
	Init(ctx context.Context, in *services.InitRequest, opts ...grpc.CallOption) (*InitResponse, error)
	Configure(ctx context.Context, in *ConfigureRequest, opts ...grpc.CallOption) (*ConfigureResponse, error)
	Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*StartResponse, error)
	Information(ctx context.Context, in *InformationRequest, opts ...grpc.CallOption) (*InformationResponse, error)
	Stop(ctx context.Context, in *StopRequest, opts ...grpc.CallOption) (*StopResponse, error)
	// Communication helper
	Communicate(ctx context.Context, in *agents.Engage, opts ...grpc.CallOption) (*agents.InformationRequest, error)
}

type runtimeClient struct {
	cc grpc.ClientConnInterface
}

func NewRuntimeClient(cc grpc.ClientConnInterface) RuntimeClient {
	return &runtimeClient{cc}
}

func (c *runtimeClient) Init(ctx context.Context, in *services.InitRequest, opts ...grpc.CallOption) (*InitResponse, error) {
	out := new(InitResponse)
	err := c.cc.Invoke(ctx, Runtime_Init_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeClient) Configure(ctx context.Context, in *ConfigureRequest, opts ...grpc.CallOption) (*ConfigureResponse, error) {
	out := new(ConfigureResponse)
	err := c.cc.Invoke(ctx, Runtime_Configure_FullMethodName, in, out, opts...)
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

func (c *runtimeClient) Information(ctx context.Context, in *InformationRequest, opts ...grpc.CallOption) (*InformationResponse, error) {
	out := new(InformationResponse)
	err := c.cc.Invoke(ctx, Runtime_Information_FullMethodName, in, out, opts...)
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

func (c *runtimeClient) Communicate(ctx context.Context, in *agents.Engage, opts ...grpc.CallOption) (*agents.InformationRequest, error) {
	out := new(agents.InformationRequest)
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
	Init(context.Context, *services.InitRequest) (*InitResponse, error)
	Configure(context.Context, *ConfigureRequest) (*ConfigureResponse, error)
	Start(context.Context, *StartRequest) (*StartResponse, error)
	Information(context.Context, *InformationRequest) (*InformationResponse, error)
	Stop(context.Context, *StopRequest) (*StopResponse, error)
	// Communication helper
	Communicate(context.Context, *agents.Engage) (*agents.InformationRequest, error)
	mustEmbedUnimplementedRuntimeServer()
}

// UnimplementedRuntimeServer must be embedded to have forward compatible implementations.
type UnimplementedRuntimeServer struct {
}

func (UnimplementedRuntimeServer) Init(context.Context, *services.InitRequest) (*InitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Init not implemented")
}
func (UnimplementedRuntimeServer) Configure(context.Context, *ConfigureRequest) (*ConfigureResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Configure not implemented")
}
func (UnimplementedRuntimeServer) Start(context.Context, *StartRequest) (*StartResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Start not implemented")
}
func (UnimplementedRuntimeServer) Information(context.Context, *InformationRequest) (*InformationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Information not implemented")
}
func (UnimplementedRuntimeServer) Stop(context.Context, *StopRequest) (*StopResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Stop not implemented")
}
func (UnimplementedRuntimeServer) Communicate(context.Context, *agents.Engage) (*agents.InformationRequest, error) {
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

func _Runtime_Init_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(services.InitRequest)
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
		return srv.(RuntimeServer).Init(ctx, req.(*services.InitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Runtime_Configure_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ConfigureRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServer).Configure(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: Runtime_Configure_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServer).Configure(ctx, req.(*ConfigureRequest))
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

func _Runtime_Communicate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(agents.Engage)
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
		return srv.(RuntimeServer).Communicate(ctx, req.(*agents.Engage))
	}
	return interceptor(ctx, in, info, handler)
}

// Runtime_ServiceDesc is the grpc.ServiceDesc for Runtime service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Runtime_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "v1.services.runtime.Runtime",
	HandlerType: (*RuntimeServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Init",
			Handler:    _Runtime_Init_Handler,
		},
		{
			MethodName: "Configure",
			Handler:    _Runtime_Configure_Handler,
		},
		{
			MethodName: "Start",
			Handler:    _Runtime_Start_Handler,
		},
		{
			MethodName: "Information",
			Handler:    _Runtime_Information_Handler,
		},
		{
			MethodName: "Stop",
			Handler:    _Runtime_Stop_Handler,
		},
		{
			MethodName: "Communicate",
			Handler:    _Runtime_Communicate_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/services/runtime/runtime.proto",
}