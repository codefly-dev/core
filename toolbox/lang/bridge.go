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
	"github.com/codefly-dev/core/toolbox/registry"
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
	b := &ToolboxFromTooling{inner: t}
	b.Base = registry.NewBase(registry.Descriptor{
		Name:           name,
		Version:        version,
		Description:    "Language toolbox (project metadata, dependencies, edits, dev validation) bridged from the Tooling contract.",
		SandboxSummary: "language tooling runs in-plugin; sandbox inherited from the agent process",
	}, b.Tools()...)
	return b
}

// ToolboxFromTooling implements toolboxv0.ToolboxServer over a
// Tooling impl. Exported so callers can compose it into a
// PluginRegistration directly.
//
// Embeds *registry.Base for ListTools/ListToolSummaries/DescribeTool;
// CallTool is overridden because the bridge dispatches per-name to
// typed Tooling RPCs (different shape than the Handler-per-tool
// pattern the capability toolboxes use). The Tools() definitions
// here therefore omit Handler — Base.CallTool would be shadowed
// anyway.
type ToolboxFromTooling struct {
	*registry.Base
	inner toolingv0.ToolingServer
}

type toolSpec struct {
	name        string
	description string
	tags        []string
	destructive bool
}

var toolSpecs = []toolSpec{
	{
		name:        ToolFix,
		description: "Auto-fix issues in a file using the language's standard fixer.",
		tags:        []string{"modification"},
		destructive: true,
	},
	{
		name:        ToolApplyEdit,
		description: "Apply a structured edit (find/replace, optionally with auto-fix) to a file.",
		tags:        []string{"modification"},
		destructive: true,
	},
	{
		name:        ToolListDependencies,
		description: "List the project's declared dependencies.",
		tags:        []string{"dependencies"},
	},
	{
		name:        ToolAddDependency,
		description: "Add a dependency to the project's manifest and install it.",
		tags:        []string{"dependencies"},
		destructive: true,
	},
	{
		name:        ToolRemoveDependency,
		description: "Remove a dependency from the project's manifest.",
		tags:        []string{"dependencies"},
		destructive: true,
	},
	{
		name:        ToolGetProjectInfo,
		description: "Get project metadata (module, language, packages, dependencies, file hashes).",
		tags:        []string{"metadata"},
	},
	{
		name:        ToolBuild,
		description: "Build the project.",
		tags:        []string{"dev"},
		destructive: true,
	},
	{
		name:        ToolTest,
		description: "Run the project's tests; returns counts + failure detail.",
		tags:        []string{"dev"},
	},
	{
		name:        ToolLint,
		description: "Lint the project; returns success/output.",
		tags:        []string{"dev"},
	},
}

// ToolNames returns the conventional lang.* names derived from the same
// specs used to register toolbox definitions.
func ToolNames() []string {
	names := make([]string, 0, len(toolSpecs))
	for _, spec := range toolSpecs {
		names = append(names, spec.name)
	}
	return names
}

// Tools projects the conventional lang.* surface into the registry's
// ToolDefinition shape — same source-of-truth pattern the
// capability toolboxes use. Bridge-specific note: the inner Tooling
// proto's typed RPCs have stable schemas, but here we surface them
// generically (anyObjectSchema) since the typed shape is enforced
// by the Mind-side wrapper at protojson encode time.
//
// Handler is left nil — CallTool is overridden below to dispatch
// per-name into the inner Tooling server's typed RPCs.
func (b *ToolboxFromTooling) Tools() []*registry.ToolDefinition {
	defs := make([]*registry.ToolDefinition, 0, len(toolSpecs))
	for _, spec := range toolSpecs {
		idem := "idempotent"
		if spec.destructive {
			idem = "side_effecting"
		}
		defs = append(defs, &registry.ToolDefinition{
			Name:               spec.name,
			SummaryDescription: spec.description,
			LongDescription:    spec.description,
			InputSchema:        anyObjectSchema(),
			Destructive:        spec.destructive,
			Tags:               toolTags(spec),
			Idempotency:        idem,
			ErrorModes:         "Mirrors the underlying typed Tooling RPC's error semantics. See proto/codefly/services/tooling/v0/tooling.proto for canonical errors per RPC.",
		})
	}
	return defs
}

func toolTags(spec toolSpec) []string {
	tags := []string{"lang", "language"}
	if spec.destructive {
		tags = append(tags, "destructive")
	} else {
		tags = append(tags, "read-only")
	}
	tags = append(tags, spec.tags...)
	return tags
}

// CallTool dispatches by conventional name. The argument Struct is
// converted to the typed proto request via protojson; the response
// is converted back to a Struct and wrapped in a Content block.
func (b *ToolboxFromTooling) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
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
// working: client.Test(ctx, req) routes to CallTool("lang.test", req)
// under the hood.
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

// All ToolingClient methods follow the same shape: hand off to callBridge
// with the right tool name and response type.

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
