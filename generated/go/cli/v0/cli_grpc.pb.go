// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: cli/v0/cli.proto

package v0

import (
	context "context"

	v01 "github.com/codefly-dev/core/generated/go/base/v0"
	v02 "github.com/codefly-dev/core/generated/go/observability/v0"
	v0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	CLI_Ping_FullMethodName                                     = "/observability.v0.CLI/Ping"
	CLI_GetAgentInformation_FullMethodName                      = "/observability.v0.CLI/GetAgentInformation"
	CLI_GetWorkspaceInventory_FullMethodName                    = "/observability.v0.CLI/GetWorkspaceInventory"
	CLI_GetWorkspaceServiceDependencyGraph_FullMethodName       = "/observability.v0.CLI/GetWorkspaceServiceDependencyGraph"
	CLI_GetWorkspacePublicModulesDependencyGraph_FullMethodName = "/observability.v0.CLI/GetWorkspacePublicModulesDependencyGraph"
	CLI_GetActive_FullMethodName                                = "/observability.v0.CLI/GetActive"
	CLI_GetAddresses_FullMethodName                             = "/observability.v0.CLI/GetAddresses"
	CLI_GetSharedConfiguration_FullMethodName                   = "/observability.v0.CLI/GetSharedConfiguration"
	CLI_Logs_FullMethodName                                     = "/observability.v0.CLI/Logs"
	CLI_ActiveLogHistory_FullMethodName                         = "/observability.v0.CLI/ActiveLogHistory"
	CLI_GetFlowStatus_FullMethodName                            = "/observability.v0.CLI/GetFlowStatus"
	CLI_StopFlow_FullMethodName                                 = "/observability.v0.CLI/StopFlow"
)

// CLIClient is the client API for CLI service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type CLIClient interface {
	Ping(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*emptypb.Empty, error)
	GetAgentInformation(ctx context.Context, in *GetAgentInformationRequest, opts ...grpc.CallOption) (*v0.AgentInformation, error)
	GetWorkspaceInventory(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v01.Workspace, error)
	GetWorkspaceServiceDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v02.GraphResponse, error)
	GetWorkspacePublicModulesDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*MultiGraphResponse, error)
	GetActive(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*ActiveResponse, error)
	GetAddresses(ctx context.Context, in *GetAddressRequest, opts ...grpc.CallOption) (*GetAddressResponse, error)
	GetSharedConfiguration(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationResponse, error)
	Logs(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (CLI_LogsClient, error)
	ActiveLogHistory(ctx context.Context, in *v02.LogRequest, opts ...grpc.CallOption) (*v02.LogResponse, error)
	GetFlowStatus(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*FlowStatus, error)
	StopFlow(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

type cLIClient struct {
	cc grpc.ClientConnInterface
}

func NewCLIClient(cc grpc.ClientConnInterface) CLIClient {
	return &cLIClient{cc}
}

func (c *cLIClient) Ping(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, CLI_Ping_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetAgentInformation(ctx context.Context, in *GetAgentInformationRequest, opts ...grpc.CallOption) (*v0.AgentInformation, error) {
	out := new(v0.AgentInformation)
	err := c.cc.Invoke(ctx, CLI_GetAgentInformation_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspaceInventory(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v01.Workspace, error) {
	out := new(v01.Workspace)
	err := c.cc.Invoke(ctx, CLI_GetWorkspaceInventory_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspaceServiceDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v02.GraphResponse, error) {
	out := new(v02.GraphResponse)
	err := c.cc.Invoke(ctx, CLI_GetWorkspaceServiceDependencyGraph_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspacePublicModulesDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*MultiGraphResponse, error) {
	out := new(MultiGraphResponse)
	err := c.cc.Invoke(ctx, CLI_GetWorkspacePublicModulesDependencyGraph_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetActive(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*ActiveResponse, error) {
	out := new(ActiveResponse)
	err := c.cc.Invoke(ctx, CLI_GetActive_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetAddresses(ctx context.Context, in *GetAddressRequest, opts ...grpc.CallOption) (*GetAddressResponse, error) {
	out := new(GetAddressResponse)
	err := c.cc.Invoke(ctx, CLI_GetAddresses_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetSharedConfiguration(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationResponse, error) {
	out := new(GetConfigurationResponse)
	err := c.cc.Invoke(ctx, CLI_GetSharedConfiguration_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) Logs(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (CLI_LogsClient, error) {
	stream, err := c.cc.NewStream(ctx, &CLI_ServiceDesc.Streams[0], CLI_Logs_FullMethodName, opts...)
	if err != nil {
		return nil, err
	}
	x := &cLILogsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type CLI_LogsClient interface {
	Recv() (*v02.Log, error)
	grpc.ClientStream
}

type cLILogsClient struct {
	grpc.ClientStream
}

func (x *cLILogsClient) Recv() (*v02.Log, error) {
	m := new(v02.Log)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *cLIClient) ActiveLogHistory(ctx context.Context, in *v02.LogRequest, opts ...grpc.CallOption) (*v02.LogResponse, error) {
	out := new(v02.LogResponse)
	err := c.cc.Invoke(ctx, CLI_ActiveLogHistory_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetFlowStatus(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*FlowStatus, error) {
	out := new(FlowStatus)
	err := c.cc.Invoke(ctx, CLI_GetFlowStatus_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) StopFlow(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, CLI_StopFlow_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CLIServer is the server API for CLI service.
// All implementations must embed UnimplementedCLIServer
// for forward compatibility
type CLIServer interface {
	Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
	GetAgentInformation(context.Context, *GetAgentInformationRequest) (*v0.AgentInformation, error)
	GetWorkspaceInventory(context.Context, *emptypb.Empty) (*v01.Workspace, error)
	GetWorkspaceServiceDependencyGraph(context.Context, *emptypb.Empty) (*v02.GraphResponse, error)
	GetWorkspacePublicModulesDependencyGraph(context.Context, *emptypb.Empty) (*MultiGraphResponse, error)
	GetActive(context.Context, *emptypb.Empty) (*ActiveResponse, error)
	GetAddresses(context.Context, *GetAddressRequest) (*GetAddressResponse, error)
	GetSharedConfiguration(context.Context, *GetConfigurationRequest) (*GetConfigurationResponse, error)
	Logs(*emptypb.Empty, CLI_LogsServer) error
	ActiveLogHistory(context.Context, *v02.LogRequest) (*v02.LogResponse, error)
	GetFlowStatus(context.Context, *emptypb.Empty) (*FlowStatus, error)
	StopFlow(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
	mustEmbedUnimplementedCLIServer()
}

// UnimplementedCLIServer must be embedded to have forward compatible implementations.
type UnimplementedCLIServer struct {
}

func (UnimplementedCLIServer) Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Ping not implemented")
}
func (UnimplementedCLIServer) GetAgentInformation(context.Context, *GetAgentInformationRequest) (*v0.AgentInformation, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetAgentInformation not implemented")
}
func (UnimplementedCLIServer) GetWorkspaceInventory(context.Context, *emptypb.Empty) (*v01.Workspace, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetWorkspaceInventory not implemented")
}
func (UnimplementedCLIServer) GetWorkspaceServiceDependencyGraph(context.Context, *emptypb.Empty) (*v02.GraphResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetWorkspaceServiceDependencyGraph not implemented")
}
func (UnimplementedCLIServer) GetWorkspacePublicModulesDependencyGraph(context.Context, *emptypb.Empty) (*MultiGraphResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetWorkspacePublicModulesDependencyGraph not implemented")
}
func (UnimplementedCLIServer) GetActive(context.Context, *emptypb.Empty) (*ActiveResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetActive not implemented")
}
func (UnimplementedCLIServer) GetAddresses(context.Context, *GetAddressRequest) (*GetAddressResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetAddresses not implemented")
}
func (UnimplementedCLIServer) GetSharedConfiguration(context.Context, *GetConfigurationRequest) (*GetConfigurationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetSharedConfiguration not implemented")
}
func (UnimplementedCLIServer) Logs(*emptypb.Empty, CLI_LogsServer) error {
	return status.Errorf(codes.Unimplemented, "method Logs not implemented")
}
func (UnimplementedCLIServer) ActiveLogHistory(context.Context, *v02.LogRequest) (*v02.LogResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ActiveLogHistory not implemented")
}
func (UnimplementedCLIServer) GetFlowStatus(context.Context, *emptypb.Empty) (*FlowStatus, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetFlowStatus not implemented")
}
func (UnimplementedCLIServer) StopFlow(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method StopFlow not implemented")
}
func (UnimplementedCLIServer) mustEmbedUnimplementedCLIServer() {}

// UnsafeCLIServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CLIServer will
// result in compilation errors.
type UnsafeCLIServer interface {
	mustEmbedUnimplementedCLIServer()
}

func RegisterCLIServer(s grpc.ServiceRegistrar, srv CLIServer) {
	s.RegisterService(&CLI_ServiceDesc, srv)
}

func _CLI_Ping_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_Ping_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).Ping(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetAgentInformation_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetAgentInformationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetAgentInformation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetAgentInformation_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetAgentInformation(ctx, req.(*GetAgentInformationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetWorkspaceInventory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetWorkspaceInventory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetWorkspaceInventory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetWorkspaceInventory(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetWorkspaceServiceDependencyGraph_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetWorkspaceServiceDependencyGraph(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetWorkspaceServiceDependencyGraph_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetWorkspaceServiceDependencyGraph(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetWorkspacePublicModulesDependencyGraph_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetWorkspacePublicModulesDependencyGraph(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetWorkspacePublicModulesDependencyGraph_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetWorkspacePublicModulesDependencyGraph(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetActive_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetActive(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetActive_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetActive(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetAddresses_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetAddressRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetAddresses(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetAddresses_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetAddresses(ctx, req.(*GetAddressRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetSharedConfiguration_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetConfigurationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetSharedConfiguration(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetSharedConfiguration_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetSharedConfiguration(ctx, req.(*GetConfigurationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_Logs_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(emptypb.Empty)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(CLIServer).Logs(m, &cLILogsServer{stream})
}

type CLI_LogsServer interface {
	Send(*v02.Log) error
	grpc.ServerStream
}

type cLILogsServer struct {
	grpc.ServerStream
}

func (x *cLILogsServer) Send(m *v02.Log) error {
	return x.ServerStream.SendMsg(m)
}

func _CLI_ActiveLogHistory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(v02.LogRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).ActiveLogHistory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_ActiveLogHistory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).ActiveLogHistory(ctx, req.(*v02.LogRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetFlowStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetFlowStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetFlowStatus_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetFlowStatus(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_StopFlow_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).StopFlow(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_StopFlow_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).StopFlow(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

// CLI_ServiceDesc is the grpc.ServiceDesc for CLI service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var CLI_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "observability.v0.CLI",
	HandlerType: (*CLIServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Ping",
			Handler:    _CLI_Ping_Handler,
		},
		{
			MethodName: "GetAgentInformation",
			Handler:    _CLI_GetAgentInformation_Handler,
		},
		{
			MethodName: "GetWorkspaceInventory",
			Handler:    _CLI_GetWorkspaceInventory_Handler,
		},
		{
			MethodName: "GetWorkspaceServiceDependencyGraph",
			Handler:    _CLI_GetWorkspaceServiceDependencyGraph_Handler,
		},
		{
			MethodName: "GetWorkspacePublicModulesDependencyGraph",
			Handler:    _CLI_GetWorkspacePublicModulesDependencyGraph_Handler,
		},
		{
			MethodName: "GetActive",
			Handler:    _CLI_GetActive_Handler,
		},
		{
			MethodName: "GetAddresses",
			Handler:    _CLI_GetAddresses_Handler,
		},
		{
			MethodName: "GetSharedConfiguration",
			Handler:    _CLI_GetSharedConfiguration_Handler,
		},
		{
			MethodName: "ActiveLogHistory",
			Handler:    _CLI_ActiveLogHistory_Handler,
		},
		{
			MethodName: "GetFlowStatus",
			Handler:    _CLI_GetFlowStatus_Handler,
		},
		{
			MethodName: "StopFlow",
			Handler:    _CLI_StopFlow_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Logs",
			Handler:       _CLI_Logs_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "cli/v0/cli.proto",
}
