package lang

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
)

// NewToolboxFromTooling adapts an existing Tooling server into a
// Toolbox server. The result implements toolboxv0.ToolboxServer; its
// Identity / ListTools / CallTool route to the underlying Tooling
// RPCs via the conventional names in names.go.
//
// Used by language agents that have a working Tooling impl and want
// to expose the unified contract WITHOUT rewriting their server. One
// line change in main.go:
//
//	agents.Serve(agents.PluginRegistration{
//	    ...,
//	    Tooling: tooling,                   // unchanged
//	    Toolbox: lang.NewToolboxFromTooling("python", "0.0.1", tooling),  // new
//	})
//
// Mind sees the same typed responses whether it calls Tooling
// directly or goes through the Toolbox via ToolingFromToolbox —
// the bridge round-trips the typed proto messages intact.
func NewToolboxFromTooling(name, version string, t toolingv0.ToolingServer) *ToolboxFromTooling {
	return &ToolboxFromTooling{name: name, version: version, inner: t}
}

// ToolboxFromTooling implements toolboxv0.ToolboxServer over a
// Tooling impl. Exported so callers can compose it into a
// PluginRegistration directly.
type ToolboxFromTooling struct {
	toolboxv0.UnimplementedToolboxServer
	name    string
	version string
	inner   toolingv0.ToolingServer
}

func (b *ToolboxFromTooling) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           b.name,
		Version:        b.version,
		Description:    "Language toolbox (LSP, analysis, dev validation) bridged from the Tooling contract.",
		SandboxSummary: "language analysis runs in-plugin; sandbox inherited from the agent process",
	}, nil
}

// ListTools enumerates the conventional surface. Schemas are minimal
// here — the typed wrapper does the heavy lifting on the Mind side.
// Toolbox callers that want richer schemas in the future can extend
// this; the bridge knows which proto type each tool maps to.
func (b *ToolboxFromTooling) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	tools := make([]*toolboxv0.Tool, 0, len(AllTools))
	for _, name := range AllTools {
		tools = append(tools, &toolboxv0.Tool{
			Name:        name,
			Description: descriptionFor(name),
			InputSchema: anyObjectSchema(),
			Destructive: isDestructive(name),
		})
	}
	return &toolboxv0.ListToolsResponse{Tools: tools}, nil
}

// CallTool dispatches by conventional name. The argument Struct is
// converted to the typed proto request via protojson; the response
// is converted back to a Struct and wrapped in a Content block.
func (b *ToolboxFromTooling) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case ToolListSymbols:
		return bridgeCall[toolingv0.ListSymbolsRequest, toolingv0.ListSymbolsResponse](ctx, req, b.inner.ListSymbols)
	case ToolGetDiagnostics:
		return bridgeCall[toolingv0.GetDiagnosticsRequest, toolingv0.GetDiagnosticsResponse](ctx, req, b.inner.GetDiagnostics)
	case ToolGoToDefinition:
		return bridgeCall[toolingv0.GoToDefinitionRequest, toolingv0.GoToDefinitionResponse](ctx, req, b.inner.GoToDefinition)
	case ToolFindReferences:
		return bridgeCall[toolingv0.FindReferencesRequest, toolingv0.FindReferencesResponse](ctx, req, b.inner.FindReferences)
	case ToolRenameSymbol:
		return bridgeCall[toolingv0.RenameSymbolRequest, toolingv0.RenameSymbolResponse](ctx, req, b.inner.RenameSymbol)
	case ToolGetHoverInfo:
		return bridgeCall[toolingv0.GetHoverInfoRequest, toolingv0.GetHoverInfoResponse](ctx, req, b.inner.GetHoverInfo)
	case ToolGetCompletions:
		return bridgeCall[toolingv0.GetCompletionsRequest, toolingv0.GetCompletionsResponse](ctx, req, b.inner.GetCompletions)
	case ToolFix:
		return bridgeCall[toolingv0.FixRequest, toolingv0.FixResponse](ctx, req, b.inner.Fix)
	case ToolApplyEdit:
		return bridgeCall[toolingv0.ApplyEditRequest, toolingv0.ApplyEditResponse](ctx, req, b.inner.ApplyEdit)
	case ToolListDependencies:
		return bridgeCall[toolingv0.ListDependenciesRequest, toolingv0.ListDependenciesResponse](ctx, req, b.inner.ListDependencies)
	case ToolAddDependency:
		return bridgeCall[toolingv0.AddDependencyRequest, toolingv0.AddDependencyResponse](ctx, req, b.inner.AddDependency)
	case ToolRemoveDependency:
		return bridgeCall[toolingv0.RemoveDependencyRequest, toolingv0.RemoveDependencyResponse](ctx, req, b.inner.RemoveDependency)
	case ToolGetProjectInfo:
		return bridgeCall[toolingv0.GetProjectInfoRequest, toolingv0.GetProjectInfoResponse](ctx, req, b.inner.GetProjectInfo)
	case ToolGetCallGraph:
		return bridgeCall[toolingv0.GetCallGraphRequest, toolingv0.GetCallGraphResponse](ctx, req, b.inner.GetCallGraph)
	case ToolBuild:
		return bridgeCall[toolingv0.BuildRequest, toolingv0.BuildResponse](ctx, req, b.inner.Build)
	case ToolTest:
		return bridgeCall[toolingv0.TestRequest, toolingv0.TestResponse](ctx, req, b.inner.Test)
	case ToolLint:
		return bridgeCall[toolingv0.LintRequest, toolingv0.LintResponse](ctx, req, b.inner.Lint)
	default:
		return &toolboxv0.CallToolResponse{
			Error: fmt.Sprintf("unknown lang tool %q (call ListTools to enumerate)", req.Name),
		}, nil
	}
}

// bridgeCall is the generic round-trip: Toolbox CallToolRequest →
// typed Tooling request → handler → typed Tooling response →
// CallToolResponse with structured Content.
//
// Generics make this exactly one function instead of seventeen
// near-identical copies. Both type parameters are constrained to
// proto.Message via the type parameter constraints (req must be a
// pointer to a struct that implements proto.Message — Go's proto
// types satisfy that).
func bridgeCall[Req any, Resp any, ReqP interface {
	*Req
	proto.Message
}, RespP interface {
	*Resp
	proto.Message
}](
	ctx context.Context,
	call *toolboxv0.CallToolRequest,
	handler func(context.Context, ReqP) (RespP, error),
) (*toolboxv0.CallToolResponse, error) {
	// Decode Toolbox arguments → typed proto request.
	var reqVal Req
	reqPtr := ReqP(&reqVal)
	if call.GetArguments() != nil {
		raw, err := protojson.Marshal(call.GetArguments())
		if err != nil {
			return errResp("bridge marshal args: %v", err), nil
		}
		if err := protojson.Unmarshal(raw, reqPtr); err != nil {
			return errResp("bridge decode args into %T: %v", reqPtr, err), nil
		}
	}

	resp, err := handler(ctx, reqPtr)
	if err != nil {
		return errResp("handler: %v", err), nil
	}

	// Encode typed response → Struct.
	out, err := protoToStruct(resp)
	if err != nil {
		return errResp("bridge encode response: %v", err), nil
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Structured{Structured: out}},
		},
	}, nil
}

// protoToStruct serializes a proto message into a structpb.Struct via
// protojson. The intermediate JSON is the canonical translation
// medium — it preserves field names, oneofs, repeated fields, and
// nested messages exactly.
func protoToStruct(m proto.Message) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var s structpb.Struct
	if err := s.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &s, nil
}

func errResp(format string, args ...any) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

// --- typed Mind wrapper -----------------------------------------

// ToolingFromToolbox wraps a Toolbox client and presents the typed
// ToolingClient interface. Mind's existing call sites continue
// working: client.ListSymbols(ctx, req) routes to
// CallTool("lang.list_symbols", req) under the hood.
//
// Returns toolingv0.ToolingClient so Mind can drop this in as a
// type-compatible replacement for a real ToolingClient over the
// Tooling RPC. When Phase α deletes the Tooling proto, this wrapper
// stays — Mind keeps its typed interface; the underlying contract
// just stops being a separate proto.
func ToolingFromToolbox(c toolboxv0.ToolboxClient) toolingv0.ToolingClient {
	return &toolingFromToolbox{c: c}
}

type toolingFromToolbox struct {
	c toolboxv0.ToolboxClient
}

// callBridge sends a typed proto request through CallTool and
// decodes the typed proto response. Inverse of bridgeCall.
func callBridge[Req proto.Message, Resp proto.Message](
	ctx context.Context, c toolboxv0.ToolboxClient,
	tool string, req Req, resp Resp,
) (Resp, error) {
	args, err := protoToStruct(req)
	if err != nil {
		var zero Resp
		return zero, fmt.Errorf("encode %s: %w", tool, err)
	}
	out, err := c.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		var zero Resp
		return zero, fmt.Errorf("CallTool %s: %w", tool, err)
	}
	if out.GetError() != "" {
		var zero Resp
		return zero, fmt.Errorf("%s: %s", tool, out.GetError())
	}
	if len(out.GetContent()) == 0 {
		// Empty response — return zero value of typed response.
		return resp, nil
	}
	s := out.GetContent()[0].GetStructured()
	if s == nil {
		var zero Resp
		return zero, fmt.Errorf("%s: response missing structured content", tool)
	}
	raw, err := protojson.Marshal(s)
	if err != nil {
		var zero Resp
		return zero, fmt.Errorf("decode %s: %w", tool, err)
	}
	if err := protojson.Unmarshal(raw, resp); err != nil {
		var zero Resp
		return zero, fmt.Errorf("decode %s into %T: %w", tool, resp, err)
	}
	return resp, nil
}

// All ToolingClient methods follow the same shape — a one-liner
// handing off to callBridge with the right tool name + response type.

func (t *toolingFromToolbox) ListSymbols(ctx context.Context, in *toolingv0.ListSymbolsRequest, _ ...grpc.CallOption) (*toolingv0.ListSymbolsResponse, error) {
	return callBridge(ctx, t.c, ToolListSymbols, in, &toolingv0.ListSymbolsResponse{})
}
func (t *toolingFromToolbox) GetDiagnostics(ctx context.Context, in *toolingv0.GetDiagnosticsRequest, _ ...grpc.CallOption) (*toolingv0.GetDiagnosticsResponse, error) {
	return callBridge(ctx, t.c, ToolGetDiagnostics, in, &toolingv0.GetDiagnosticsResponse{})
}
func (t *toolingFromToolbox) GoToDefinition(ctx context.Context, in *toolingv0.GoToDefinitionRequest, _ ...grpc.CallOption) (*toolingv0.GoToDefinitionResponse, error) {
	return callBridge(ctx, t.c, ToolGoToDefinition, in, &toolingv0.GoToDefinitionResponse{})
}
func (t *toolingFromToolbox) FindReferences(ctx context.Context, in *toolingv0.FindReferencesRequest, _ ...grpc.CallOption) (*toolingv0.FindReferencesResponse, error) {
	return callBridge(ctx, t.c, ToolFindReferences, in, &toolingv0.FindReferencesResponse{})
}
func (t *toolingFromToolbox) RenameSymbol(ctx context.Context, in *toolingv0.RenameSymbolRequest, _ ...grpc.CallOption) (*toolingv0.RenameSymbolResponse, error) {
	return callBridge(ctx, t.c, ToolRenameSymbol, in, &toolingv0.RenameSymbolResponse{})
}
func (t *toolingFromToolbox) GetHoverInfo(ctx context.Context, in *toolingv0.GetHoverInfoRequest, _ ...grpc.CallOption) (*toolingv0.GetHoverInfoResponse, error) {
	return callBridge(ctx, t.c, ToolGetHoverInfo, in, &toolingv0.GetHoverInfoResponse{})
}
func (t *toolingFromToolbox) GetCompletions(ctx context.Context, in *toolingv0.GetCompletionsRequest, _ ...grpc.CallOption) (*toolingv0.GetCompletionsResponse, error) {
	return callBridge(ctx, t.c, ToolGetCompletions, in, &toolingv0.GetCompletionsResponse{})
}
func (t *toolingFromToolbox) Fix(ctx context.Context, in *toolingv0.FixRequest, _ ...grpc.CallOption) (*toolingv0.FixResponse, error) {
	return callBridge(ctx, t.c, ToolFix, in, &toolingv0.FixResponse{})
}
func (t *toolingFromToolbox) ApplyEdit(ctx context.Context, in *toolingv0.ApplyEditRequest, _ ...grpc.CallOption) (*toolingv0.ApplyEditResponse, error) {
	return callBridge(ctx, t.c, ToolApplyEdit, in, &toolingv0.ApplyEditResponse{})
}
func (t *toolingFromToolbox) ListDependencies(ctx context.Context, in *toolingv0.ListDependenciesRequest, _ ...grpc.CallOption) (*toolingv0.ListDependenciesResponse, error) {
	return callBridge(ctx, t.c, ToolListDependencies, in, &toolingv0.ListDependenciesResponse{})
}
func (t *toolingFromToolbox) AddDependency(ctx context.Context, in *toolingv0.AddDependencyRequest, _ ...grpc.CallOption) (*toolingv0.AddDependencyResponse, error) {
	return callBridge(ctx, t.c, ToolAddDependency, in, &toolingv0.AddDependencyResponse{})
}
func (t *toolingFromToolbox) RemoveDependency(ctx context.Context, in *toolingv0.RemoveDependencyRequest, _ ...grpc.CallOption) (*toolingv0.RemoveDependencyResponse, error) {
	return callBridge(ctx, t.c, ToolRemoveDependency, in, &toolingv0.RemoveDependencyResponse{})
}
func (t *toolingFromToolbox) GetProjectInfo(ctx context.Context, in *toolingv0.GetProjectInfoRequest, _ ...grpc.CallOption) (*toolingv0.GetProjectInfoResponse, error) {
	return callBridge(ctx, t.c, ToolGetProjectInfo, in, &toolingv0.GetProjectInfoResponse{})
}
func (t *toolingFromToolbox) GetCallGraph(ctx context.Context, in *toolingv0.GetCallGraphRequest, _ ...grpc.CallOption) (*toolingv0.GetCallGraphResponse, error) {
	return callBridge(ctx, t.c, ToolGetCallGraph, in, &toolingv0.GetCallGraphResponse{})
}
func (t *toolingFromToolbox) Build(ctx context.Context, in *toolingv0.BuildRequest, _ ...grpc.CallOption) (*toolingv0.BuildResponse, error) {
	return callBridge(ctx, t.c, ToolBuild, in, &toolingv0.BuildResponse{})
}
func (t *toolingFromToolbox) Test(ctx context.Context, in *toolingv0.TestRequest, _ ...grpc.CallOption) (*toolingv0.TestResponse, error) {
	return callBridge(ctx, t.c, ToolTest, in, &toolingv0.TestResponse{})
}
func (t *toolingFromToolbox) Lint(ctx context.Context, in *toolingv0.LintRequest, _ ...grpc.CallOption) (*toolingv0.LintResponse, error) {
	return callBridge(ctx, t.c, ToolLint, in, &toolingv0.LintResponse{})
}

// --- helpers ----------------------------------------------------

// anyObjectSchema is the JSON-Schema "object" with no required
// fields — every typed Tooling request accepts the same shape via
// protojson, so the schema is uniform.
func anyObjectSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":                 "object",
		"additionalProperties": true,
	})
	return s
}

// descriptionFor returns the human description for a conventional
// tool name. Not load-bearing — Identity / catalog UI consume it.
func descriptionFor(tool string) string {
	switch tool {
	case ToolListSymbols:
		return "List symbols (functions, classes, variables) in a file via the language's LSP."
	case ToolGetDiagnostics:
		return "Get diagnostics (errors, warnings) for a file via the language's LSP."
	case ToolGoToDefinition:
		return "Resolve a symbol reference to its definition site."
	case ToolFindReferences:
		return "Find all references to a symbol across the project."
	case ToolRenameSymbol:
		return "Rename a symbol across the project (LSP-validated)."
	case ToolGetHoverInfo:
		return "Get hover information (type, doc) for a position in a file."
	case ToolGetCompletions:
		return "Get completion candidates for a position in a file."
	case ToolFix:
		return "Auto-fix issues in a file using the language's standard fixer."
	case ToolApplyEdit:
		return "Apply a structured edit (find/replace, optionally with auto-fix) to a file."
	case ToolListDependencies:
		return "List the project's declared dependencies."
	case ToolAddDependency:
		return "Add a dependency to the project's manifest and install it."
	case ToolRemoveDependency:
		return "Remove a dependency from the project's manifest."
	case ToolGetProjectInfo:
		return "Get project metadata (module, language, packages, dependencies, file hashes)."
	case ToolGetCallGraph:
		return "Compute the call graph (caller/callee relationships) across the project."
	case ToolBuild:
		return "Build the project."
	case ToolTest:
		return "Run the project's tests; returns counts + failure detail."
	case ToolLint:
		return "Lint the project; returns success/output."
	default:
		return ""
	}
}

// isDestructive marks tools whose default execution mutates state.
// Hosts apply extra confirmation UI for these.
func isDestructive(tool string) bool {
	switch tool {
	case ToolRenameSymbol, ToolFix, ToolApplyEdit,
		ToolAddDependency, ToolRemoveDependency, ToolBuild:
		return true
	}
	return false
}
