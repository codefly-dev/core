package grpc

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/internal/respond"
)

// DefaultDialTimeout caps any single dial+reflect call. gRPC dials
// otherwise wait forever for a connection. Configurable per-call via
// the timeout_ms argument.
const DefaultDialTimeout = 10 * time.Second

// Server implements codefly.services.toolbox.v0.Toolbox for gRPC
// reflection-based introspection.
//
// Construction is cheap; the toolbox holds no persistent connection.
// Each tool call dials the target, performs the reflection
// roundtrip, and tears the connection down. This is the safe-by-
// default position — connections are short-lived and there's no
// state for an attacker (or a buggy agent) to leak across calls.
type Server struct {
	toolboxv0.UnimplementedToolboxServer

	version string
}

// New returns a Server.
func New(version string) *Server {
	return &Server{version: version}
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "grpc",
		Version:        s.version,
		Description:    "gRPC reflection-based service/method introspection. Canonical owner of the `grpcurl` binary.",
		CanonicalFor:   []string{"grpcurl"},
		SandboxSummary: "reads: deny; writes: deny; network: allowed to the dial target (one short-lived connection per call)",
	}, nil
}

// --- Tools -------------------------------------------------------

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	addrSchema := map[string]any{
		"type":        "string",
		"description": "host:port of the gRPC server (no scheme).",
	}
	timeoutSchema := map[string]any{
		"type":        "integer",
		"description": "Per-call dial timeout in ms. Default 10000.",
		"minimum":     100,
		"maximum":     60000,
	}

	return &toolboxv0.ListToolsResponse{
		Tools: []*toolboxv0.Tool{
			{
				Name:        "grpc.list_services",
				Description: "Connect to a gRPC server and list every service it exposes via reflection.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address":    addrSchema,
						"timeout_ms": timeoutSchema,
					},
					"required": []any{"address"},
				}),
				Destructive: false,
			},
			{
				Name:        "grpc.describe_service",
				Description: "List the methods of a single service (and their request/response type names) via reflection.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address": addrSchema,
						"service": map[string]any{
							"type":        "string",
							"description": "Fully-qualified service name, e.g. `helloworld.Greeter`.",
						},
						"timeout_ms": timeoutSchema,
					},
					"required": []any{"address", "service"},
				}),
				Destructive: false,
			},
			{
				Name:        "grpc.describe_method",
				Description: "Describe a single method on a service: full input/output message names. Useful before composing a call.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address": addrSchema,
						"service": map[string]any{
							"type":        "string",
							"description": "Fully-qualified service name.",
						},
						"method": map[string]any{
							"type":        "string",
							"description": "Method name within the service.",
						},
						"timeout_ms": timeoutSchema,
					},
					"required": []any{"address", "service", "method"},
				}),
				Destructive: false,
			},
			{
				Name:        "grpc.call",
				Description: "Invoke a unary RPC with JSON-shaped arguments. Phase 2 — currently a stub that documents the contract.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address": addrSchema,
						"service": map[string]any{"type": "string"},
						"method":  map[string]any{"type": "string"},
						"request": map[string]any{
							"type":        "object",
							"description": "Request body as a JSON object matching the method's input message.",
						},
						"timeout_ms": timeoutSchema,
					},
					"required": []any{"address", "service", "method"},
				}),
				Destructive: false,
			},
		},
	}, nil
}

func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "grpc.list_services":
		return s.listServices(ctx, req)
	case "grpc.describe_service":
		return s.describeService(ctx, req)
	case "grpc.describe_method":
		return s.describeMethod(ctx, req)
	case "grpc.call":
		// Phase 2 stub — see doc.go. The dispatch path is here so a
		// later commit only swaps the body, not the surface.
		return respond.Error("grpc.call not yet implemented; introspection (list_services / describe_service / describe_method) is usable today"), nil
	default:
		return respond.Error("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) listServices(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	address, ok := args["address"].(string)
	if !ok || address == "" {
		return respond.Error("grpc.list_services: address is required"), nil
	}
	timeout := timeoutFromArgs(args)

	services, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]string, error) {
		return reflectListServices(stream)
	})
	if err != nil {
		return respond.Error("grpc.list_services: %v", err), nil
	}

	// Sort for determinism — agents diff'ing successive calls
	// shouldn't see spurious churn just because the server walked the
	// service map in a different order.
	sort.Strings(services)
	out := make([]any, len(services))
	for i, sv := range services {
		out[i] = sv
	}
	return respond.Struct(map[string]any{"services": out}), nil
}

func (s *Server) describeService(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	address, _ := args["address"].(string)
	service, _ := args["service"].(string)
	if address == "" || service == "" {
		return respond.Error("grpc.describe_service: address and service are required"), nil
	}
	timeout := timeoutFromArgs(args)

	methods, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]methodInfo, error) {
		return reflectDescribeService(stream, service)
	})
	if err != nil {
		return respond.Error("grpc.describe_service: %v", err), nil
	}

	out := make([]any, len(methods))
	for i, m := range methods {
		out[i] = map[string]any{
			"name":             m.Name,
			"input_type":       m.InputType,
			"output_type":      m.OutputType,
			"client_streaming": m.ClientStreaming,
			"server_streaming": m.ServerStreaming,
		}
	}
	return respond.Struct(map[string]any{
		"service": service,
		"methods": out,
	}), nil
}

func (s *Server) describeMethod(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	address, _ := args["address"].(string)
	service, _ := args["service"].(string)
	method, _ := args["method"].(string)
	if address == "" || service == "" || method == "" {
		return respond.Error("grpc.describe_method: address, service, and method are required"), nil
	}
	timeout := timeoutFromArgs(args)

	info, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) (*methodInfo, error) {
		methods, err := reflectDescribeService(stream, service)
		if err != nil {
			return nil, err
		}
		for _, m := range methods {
			if m.Name == method {
				m := m
				return &m, nil
			}
		}
		return nil, fmt.Errorf("method %q not found on service %q", method, service)
	})
	if err != nil {
		return respond.Error("grpc.describe_method: %v", err), nil
	}

	return respond.Struct(map[string]any{
		"service":          service,
		"name":             info.Name,
		"input_type":       info.InputType,
		"output_type":      info.OutputType,
		"client_streaming": info.ClientStreaming,
		"server_streaming": info.ServerStreaming,
	}), nil
}

// --- Reflection plumbing -----------------------------------------

// methodInfo is the toolbox's own (lightweight) view of a method —
// just the fields callers care about, decoupled from the proto
// descriptor types so the response shape is JSON-stable.
type methodInfo struct {
	Name            string
	InputType       string
	OutputType      string
	ClientStreaming bool
	ServerStreaming bool
}

// withReflectStream dials the target, opens a reflection stream,
// runs fn against it, and tears everything down. Generic over the
// caller's return type so each tool gets typed results without
// rewriting the dial-and-stream boilerplate.
func withReflectStream[T any](
	ctx context.Context, address string, timeout time.Duration,
	fn func(reflectpb.ServerReflection_ServerReflectionInfoClient) (T, error),
) (T, error) {
	var zero T
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// NewClient + insecure creds: we only do read-side reflection;
	// the policy decision about TLS vs insecure is the host's, not
	// the toolbox's. A future iteration can take a TLS config arg.
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return zero, fmt.Errorf("dial %s: %w", address, err)
	}
	defer conn.Close()

	client := reflectpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(dialCtx)
	if err != nil {
		return zero, fmt.Errorf("open reflection stream: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	return fn(stream)
}

// reflectListServices issues a ListServices request and reads the
// single reply. The reflection stream is bidirectional but each
// query is a request/response pair; we don't pipeline.
func reflectListServices(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]string, error) {
	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, fmt.Errorf("send ListServices: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("server closed reflection stream without reply")
		}
		return nil, fmt.Errorf("recv ListServices: %w", err)
	}
	if errResp := resp.GetErrorResponse(); errResp != nil {
		return nil, fmt.Errorf("reflection: %s (code %d)", errResp.GetErrorMessage(), errResp.GetErrorCode())
	}
	listResp := resp.GetListServicesResponse()
	if listResp == nil {
		return nil, fmt.Errorf("reflection: ListServicesResponse missing")
	}
	out := make([]string, 0, len(listResp.GetService()))
	for _, sv := range listResp.GetService() {
		out = append(out, sv.GetName())
	}
	return out, nil
}

// reflectDescribeService asks the server for the FileDescriptor
// containing the named service, parses it, and returns the methods.
// FileContainingSymbol is the standard "give me the file that
// defines X" reflection query — same one grpcurl uses internally.
func reflectDescribeService(stream reflectpb.ServerReflection_ServerReflectionInfoClient, service string) ([]methodInfo, error) {
	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: service,
		},
	}); err != nil {
		return nil, fmt.Errorf("send FileContainingSymbol: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv FileContainingSymbol: %w", err)
	}
	if errResp := resp.GetErrorResponse(); errResp != nil {
		return nil, fmt.Errorf("reflection: %s (code %d)", errResp.GetErrorMessage(), errResp.GetErrorCode())
	}
	fd := resp.GetFileDescriptorResponse()
	if fd == nil {
		return nil, fmt.Errorf("reflection: FileDescriptorResponse missing")
	}

	// The reflection server may return the requested file plus its
	// transitive dependencies. Find the one that actually defines
	// the requested service.
	for _, raw := range fd.GetFileDescriptorProto() {
		var fdp descriptorpb.FileDescriptorProto
		if err := proto.Unmarshal(raw, &fdp); err != nil {
			return nil, fmt.Errorf("unmarshal FileDescriptorProto: %w", err)
		}
		// service is fully-qualified ("pkg.Service"); fdp.Package is
		// "pkg" and Service.Name is "Service".
		shortName := service
		if fdp.GetPackage() != "" {
			prefix := fdp.GetPackage() + "."
			if strings.HasPrefix(service, prefix) {
				shortName = strings.TrimPrefix(service, prefix)
			} else {
				continue
			}
		}
		for _, sd := range fdp.GetService() {
			if sd.GetName() != shortName {
				continue
			}
			methods := make([]methodInfo, 0, len(sd.GetMethod()))
			for _, md := range sd.GetMethod() {
				methods = append(methods, methodInfo{
					Name:            md.GetName(),
					InputType:       strings.TrimPrefix(md.GetInputType(), "."),
					OutputType:      strings.TrimPrefix(md.GetOutputType(), "."),
					ClientStreaming: md.GetClientStreaming(),
					ServerStreaming: md.GetServerStreaming(),
				})
			}
			return methods, nil
		}
	}
	return nil, fmt.Errorf("service %q not found in any returned FileDescriptorProto", service)
}

// timeoutFromArgs reads the timeout_ms argument with the toolbox's
// default floor. Callers always get a positive duration.
func timeoutFromArgs(args map[string]any) time.Duration {
	if v, ok := args["timeout_ms"].(float64); ok && v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return DefaultDialTimeout
}
