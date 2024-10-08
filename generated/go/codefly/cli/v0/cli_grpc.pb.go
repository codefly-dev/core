// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             (unknown)
// source: codefly/cli/v0/cli.proto

package v0

import (
	context "context"
	v01 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v02 "github.com/codefly-dev/core/generated/go/codefly/observability/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	CLI_Ping_FullMethodName                                     = "/codefly.cli.v0.CLI/Ping"
	CLI_GetAgentInformation_FullMethodName                      = "/codefly.cli.v0.CLI/GetAgentInformation"
	CLI_GetWorkspaceInventory_FullMethodName                    = "/codefly.cli.v0.CLI/GetWorkspaceInventory"
	CLI_GetWorkspaceServiceDependencyGraph_FullMethodName       = "/codefly.cli.v0.CLI/GetWorkspaceServiceDependencyGraph"
	CLI_GetWorkspacePublicModulesDependencyGraph_FullMethodName = "/codefly.cli.v0.CLI/GetWorkspacePublicModulesDependencyGraph"
	CLI_GetActive_FullMethodName                                = "/codefly.cli.v0.CLI/GetActive"
	CLI_GetAddresses_FullMethodName                             = "/codefly.cli.v0.CLI/GetAddresses"
	CLI_GetConfiguration_FullMethodName                         = "/codefly.cli.v0.CLI/GetConfiguration"
	CLI_GetDependenciesConfigurations_FullMethodName            = "/codefly.cli.v0.CLI/GetDependenciesConfigurations"
	CLI_GetDependenciesNetworkMappings_FullMethodName           = "/codefly.cli.v0.CLI/GetDependenciesNetworkMappings"
	CLI_GetRuntimeConfigurations_FullMethodName                 = "/codefly.cli.v0.CLI/GetRuntimeConfigurations"
	CLI_Logs_FullMethodName                                     = "/codefly.cli.v0.CLI/Logs"
	CLI_ActiveLogHistory_FullMethodName                         = "/codefly.cli.v0.CLI/ActiveLogHistory"
	CLI_GetFlowStatus_FullMethodName                            = "/codefly.cli.v0.CLI/GetFlowStatus"
	CLI_StopFlow_FullMethodName                                 = "/codefly.cli.v0.CLI/StopFlow"
	CLI_DestroyFlow_FullMethodName                              = "/codefly.cli.v0.CLI/DestroyFlow"
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
	GetConfiguration(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationResponse, error)
	GetDependenciesConfigurations(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationsResponse, error)
	GetDependenciesNetworkMappings(ctx context.Context, in *GetNetworkMappingsRequest, opts ...grpc.CallOption) (*GetNetworkMappingsResponse, error)
	GetRuntimeConfigurations(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationsResponse, error)
	Logs(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (grpc.ServerStreamingClient[v02.Log], error)
	ActiveLogHistory(ctx context.Context, in *v02.LogRequest, opts ...grpc.CallOption) (*v02.LogResponse, error)
	GetFlowStatus(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*FlowStatus, error)
	StopFlow(ctx context.Context, in *StopFlowRequest, opts ...grpc.CallOption) (*StopFlowResponse, error)
	DestroyFlow(ctx context.Context, in *DestroyFlowRequest, opts ...grpc.CallOption) (*DestroyFlowResponse, error)
}

type cLIClient struct {
	cc grpc.ClientConnInterface
}

func NewCLIClient(cc grpc.ClientConnInterface) CLIClient {
	return &cLIClient{cc}
}

func (c *cLIClient) Ping(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, CLI_Ping_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetAgentInformation(ctx context.Context, in *GetAgentInformationRequest, opts ...grpc.CallOption) (*v0.AgentInformation, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(v0.AgentInformation)
	err := c.cc.Invoke(ctx, CLI_GetAgentInformation_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspaceInventory(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v01.Workspace, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(v01.Workspace)
	err := c.cc.Invoke(ctx, CLI_GetWorkspaceInventory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspaceServiceDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*v02.GraphResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(v02.GraphResponse)
	err := c.cc.Invoke(ctx, CLI_GetWorkspaceServiceDependencyGraph_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetWorkspacePublicModulesDependencyGraph(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*MultiGraphResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(MultiGraphResponse)
	err := c.cc.Invoke(ctx, CLI_GetWorkspacePublicModulesDependencyGraph_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetActive(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*ActiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ActiveResponse)
	err := c.cc.Invoke(ctx, CLI_GetActive_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetAddresses(ctx context.Context, in *GetAddressRequest, opts ...grpc.CallOption) (*GetAddressResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetAddressResponse)
	err := c.cc.Invoke(ctx, CLI_GetAddresses_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetConfiguration(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetConfigurationResponse)
	err := c.cc.Invoke(ctx, CLI_GetConfiguration_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetDependenciesConfigurations(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetConfigurationsResponse)
	err := c.cc.Invoke(ctx, CLI_GetDependenciesConfigurations_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetDependenciesNetworkMappings(ctx context.Context, in *GetNetworkMappingsRequest, opts ...grpc.CallOption) (*GetNetworkMappingsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetNetworkMappingsResponse)
	err := c.cc.Invoke(ctx, CLI_GetDependenciesNetworkMappings_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetRuntimeConfigurations(ctx context.Context, in *GetConfigurationRequest, opts ...grpc.CallOption) (*GetConfigurationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetConfigurationsResponse)
	err := c.cc.Invoke(ctx, CLI_GetRuntimeConfigurations_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) Logs(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (grpc.ServerStreamingClient[v02.Log], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &CLI_ServiceDesc.Streams[0], CLI_Logs_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[emptypb.Empty, v02.Log]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type CLI_LogsClient = grpc.ServerStreamingClient[v02.Log]

func (c *cLIClient) ActiveLogHistory(ctx context.Context, in *v02.LogRequest, opts ...grpc.CallOption) (*v02.LogResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(v02.LogResponse)
	err := c.cc.Invoke(ctx, CLI_ActiveLogHistory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) GetFlowStatus(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*FlowStatus, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(FlowStatus)
	err := c.cc.Invoke(ctx, CLI_GetFlowStatus_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) StopFlow(ctx context.Context, in *StopFlowRequest, opts ...grpc.CallOption) (*StopFlowResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(StopFlowResponse)
	err := c.cc.Invoke(ctx, CLI_StopFlow_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cLIClient) DestroyFlow(ctx context.Context, in *DestroyFlowRequest, opts ...grpc.CallOption) (*DestroyFlowResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DestroyFlowResponse)
	err := c.cc.Invoke(ctx, CLI_DestroyFlow_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CLIServer is the server API for CLI service.
// All implementations must embed UnimplementedCLIServer
// for forward compatibility.
type CLIServer interface {
	Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
	GetAgentInformation(context.Context, *GetAgentInformationRequest) (*v0.AgentInformation, error)
	GetWorkspaceInventory(context.Context, *emptypb.Empty) (*v01.Workspace, error)
	GetWorkspaceServiceDependencyGraph(context.Context, *emptypb.Empty) (*v02.GraphResponse, error)
	GetWorkspacePublicModulesDependencyGraph(context.Context, *emptypb.Empty) (*MultiGraphResponse, error)
	GetActive(context.Context, *emptypb.Empty) (*ActiveResponse, error)
	GetAddresses(context.Context, *GetAddressRequest) (*GetAddressResponse, error)
	GetConfiguration(context.Context, *GetConfigurationRequest) (*GetConfigurationResponse, error)
	GetDependenciesConfigurations(context.Context, *GetConfigurationRequest) (*GetConfigurationsResponse, error)
	GetDependenciesNetworkMappings(context.Context, *GetNetworkMappingsRequest) (*GetNetworkMappingsResponse, error)
	GetRuntimeConfigurations(context.Context, *GetConfigurationRequest) (*GetConfigurationsResponse, error)
	Logs(*emptypb.Empty, grpc.ServerStreamingServer[v02.Log]) error
	ActiveLogHistory(context.Context, *v02.LogRequest) (*v02.LogResponse, error)
	GetFlowStatus(context.Context, *emptypb.Empty) (*FlowStatus, error)
	StopFlow(context.Context, *StopFlowRequest) (*StopFlowResponse, error)
	DestroyFlow(context.Context, *DestroyFlowRequest) (*DestroyFlowResponse, error)
	mustEmbedUnimplementedCLIServer()
}

// UnimplementedCLIServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedCLIServer struct{}

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
func (UnimplementedCLIServer) GetConfiguration(context.Context, *GetConfigurationRequest) (*GetConfigurationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetConfiguration not implemented")
}
func (UnimplementedCLIServer) GetDependenciesConfigurations(context.Context, *GetConfigurationRequest) (*GetConfigurationsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetDependenciesConfigurations not implemented")
}
func (UnimplementedCLIServer) GetDependenciesNetworkMappings(context.Context, *GetNetworkMappingsRequest) (*GetNetworkMappingsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetDependenciesNetworkMappings not implemented")
}
func (UnimplementedCLIServer) GetRuntimeConfigurations(context.Context, *GetConfigurationRequest) (*GetConfigurationsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetRuntimeConfigurations not implemented")
}
func (UnimplementedCLIServer) Logs(*emptypb.Empty, grpc.ServerStreamingServer[v02.Log]) error {
	return status.Errorf(codes.Unimplemented, "method Logs not implemented")
}
func (UnimplementedCLIServer) ActiveLogHistory(context.Context, *v02.LogRequest) (*v02.LogResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ActiveLogHistory not implemented")
}
func (UnimplementedCLIServer) GetFlowStatus(context.Context, *emptypb.Empty) (*FlowStatus, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetFlowStatus not implemented")
}
func (UnimplementedCLIServer) StopFlow(context.Context, *StopFlowRequest) (*StopFlowResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method StopFlow not implemented")
}
func (UnimplementedCLIServer) DestroyFlow(context.Context, *DestroyFlowRequest) (*DestroyFlowResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DestroyFlow not implemented")
}
func (UnimplementedCLIServer) mustEmbedUnimplementedCLIServer() {}
func (UnimplementedCLIServer) testEmbeddedByValue()             {}

// UnsafeCLIServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CLIServer will
// result in compilation errors.
type UnsafeCLIServer interface {
	mustEmbedUnimplementedCLIServer()
}

func RegisterCLIServer(s grpc.ServiceRegistrar, srv CLIServer) {
	// If the following call pancis, it indicates UnimplementedCLIServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
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

func _CLI_GetConfiguration_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetConfigurationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetConfiguration(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetConfiguration_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetConfiguration(ctx, req.(*GetConfigurationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetDependenciesConfigurations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetConfigurationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetDependenciesConfigurations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetDependenciesConfigurations_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetDependenciesConfigurations(ctx, req.(*GetConfigurationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetDependenciesNetworkMappings_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetNetworkMappingsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetDependenciesNetworkMappings(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetDependenciesNetworkMappings_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetDependenciesNetworkMappings(ctx, req.(*GetNetworkMappingsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_GetRuntimeConfigurations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetConfigurationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).GetRuntimeConfigurations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_GetRuntimeConfigurations_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).GetRuntimeConfigurations(ctx, req.(*GetConfigurationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_Logs_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(emptypb.Empty)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(CLIServer).Logs(m, &grpc.GenericServerStream[emptypb.Empty, v02.Log]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type CLI_LogsServer = grpc.ServerStreamingServer[v02.Log]

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
	in := new(StopFlowRequest)
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
		return srv.(CLIServer).StopFlow(ctx, req.(*StopFlowRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CLI_DestroyFlow_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DestroyFlowRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CLIServer).DestroyFlow(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CLI_DestroyFlow_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CLIServer).DestroyFlow(ctx, req.(*DestroyFlowRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// CLI_ServiceDesc is the grpc.ServiceDesc for CLI service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var CLI_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "codefly.cli.v0.CLI",
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
			MethodName: "GetConfiguration",
			Handler:    _CLI_GetConfiguration_Handler,
		},
		{
			MethodName: "GetDependenciesConfigurations",
			Handler:    _CLI_GetDependenciesConfigurations_Handler,
		},
		{
			MethodName: "GetDependenciesNetworkMappings",
			Handler:    _CLI_GetDependenciesNetworkMappings_Handler,
		},
		{
			MethodName: "GetRuntimeConfigurations",
			Handler:    _CLI_GetRuntimeConfigurations_Handler,
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
		{
			MethodName: "DestroyFlow",
			Handler:    _CLI_DestroyFlow_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Logs",
			Handler:       _CLI_Logs_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "codefly/cli/v0/cli.proto",
}
