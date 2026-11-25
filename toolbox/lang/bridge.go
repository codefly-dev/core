package lang

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv1 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v1"
	toolingv1 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v1"
	"github.com/codefly-dev/core/toolbox/registry"
)

// NewToolboxFromTooling adapts an existing Tooling server into a
// Toolbox server. The result implements toolboxv1.ToolboxServer; its
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
func NewToolboxFromTooling(name, version string, t toolingv1.ToolingServer) *ToolboxFromTooling {
	return newToolboxFromTooling(name, version, t, toolSpecs)
}

// NewSourceToolboxFromTooling exposes only the source-authoring operations
// implemented by code.SourceTooling. This keeps ListTools honest for small
// language plugins that format and edit source but do not own dependency or
// validation behavior yet.
func NewSourceToolboxFromTooling(name, version string, t toolingv1.ToolingServer) *ToolboxFromTooling {
	return newToolboxFromTooling(name, version, t, selectToolSpecs(ToolFix, ToolApplyEdit, ToolGetProjectInfo, ToolGetSemanticIndex, ToolGetInstructionIndex))
}

// NewValidationToolboxFromTooling exposes source authoring plus build, test,
// and lint for language plugins that do not implement dependency mutation.
func NewValidationToolboxFromTooling(name, version string, t toolingv1.ToolingServer) *ToolboxFromTooling {
	return newToolboxFromTooling(name, version, t, selectToolSpecs(
		ToolFix, ToolApplyEdit, ToolGetProjectInfo, ToolGetSemanticIndex, ToolGetInstructionIndex, ToolBuild, ToolTest, ToolLint,
	))
}

// NewEditToolboxFromTooling is the language-neutral subset for a plugin that
// supports structured editing and metadata but has no language-aware fixer.
func NewEditToolboxFromTooling(name, version string, t toolingv1.ToolingServer) *ToolboxFromTooling {
	return newToolboxFromTooling(name, version, t, selectToolSpecs(ToolApplyEdit, ToolGetProjectInfo, ToolGetSemanticIndex, ToolGetInstructionIndex))
}

func newToolboxFromTooling(name, version string, t toolingv1.ToolingServer, specs []toolSpec) *ToolboxFromTooling {
	b := &ToolboxFromTooling{inner: t, specs: specs, allowed: make(map[string]struct{}, len(specs))}
	for _, spec := range specs {
		b.allowed[spec.name] = struct{}{}
	}
	b.Base = registry.NewBase(registry.Descriptor{
		Name:           name,
		Version:        version,
		Description:    "Language toolbox (project metadata, dependencies, edits, dev validation) bridged from the Tooling contract.",
		SandboxSummary: "language tooling runs in-plugin; sandbox inherited from the agent process",
	}, b.Tools()...)
	return b
}

// ToolboxFromTooling implements toolboxv1.ToolboxServer over a
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
	inner   toolingv1.ToolingServer
	specs   []toolSpec
	allowed map[string]struct{}
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
		description: "Format and safely repair one source file. SAFE is the default; AGGRESSIVE must be explicit. Supports evidence-only dry runs.",
		tags:        []string{"modification", "source", "safe-by-default"},
		destructive: true,
	},
	{
		name:        ToolApplyEdit,
		description: "Apply a structured edit and, by default, the language's safe source fixer in one staged write. Supports evidence-only dry runs.",
		tags:        []string{"modification", "source", "safe-by-default"},
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
		name:        ToolGetSemanticIndex,
		description: "Project source into body-free semantic files, symbols, calls, references, hashes, and typed coverage.",
		tags:        []string{"metadata", "semantic", "analysis"},
	},
	{
		name:        ToolGetInstructionIndex,
		description: "Project recognized repository instruction documents into typed, scoped guidance and body-free source identities.",
		tags:        []string{"metadata", "instructions", "analysis"},
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

func selectToolSpecs(names ...string) []toolSpec {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	selected := make([]toolSpec, 0, len(names))
	for _, spec := range toolSpecs {
		if _, ok := wanted[spec.name]; ok {
			selected = append(selected, spec)
		}
	}
	return selected
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
	defs := make([]*registry.ToolDefinition, 0, len(b.specs))
	for _, spec := range b.specs {
		idem := "idempotent"
		if spec.destructive {
			idem = "side_effecting"
		}
		defs = append(defs, &registry.ToolDefinition{
			Name:               spec.name,
			SummaryDescription: spec.description,
			LongDescription:    spec.description,
			InputSchema:        schemaForTool(spec.name),
			Destructive:        spec.destructive,
			Tags:               toolTags(spec),
			Idempotency:        idem,
			ErrorModes:         "Mirrors the underlying typed Tooling RPC's error semantics. See proto/codefly/services/tooling/v1/tooling.proto for canonical errors per RPC.",
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
func (b *ToolboxFromTooling) CallTool(ctx context.Context, req *toolboxv1.CallToolRequest) (*toolboxv1.CallToolResponse, error) {
	if _, ok := b.allowed[req.GetName()]; !ok {
		return &toolboxv1.CallToolResponse{Error: fmt.Sprintf("unknown lang tool %q (call ListTools to enumerate)", req.GetName())}, nil
	}
	switch req.Name {
	case ToolFix:
		return bridgeCall[toolingv1.FixRequest, toolingv1.FixResponse](ctx, req, b.inner.Fix)
	case ToolApplyEdit:
		return bridgeCall[toolingv1.ApplyEditRequest, toolingv1.ApplyEditResponse](ctx, req, b.inner.ApplyEdit)
	case ToolListDependencies:
		return bridgeCall[toolingv1.ListDependenciesRequest, toolingv1.ListDependenciesResponse](ctx, req, b.inner.ListDependencies)
	case ToolAddDependency:
		return bridgeCall[toolingv1.AddDependencyRequest, toolingv1.AddDependencyResponse](ctx, req, b.inner.AddDependency)
	case ToolRemoveDependency:
		return bridgeCall[toolingv1.RemoveDependencyRequest, toolingv1.RemoveDependencyResponse](ctx, req, b.inner.RemoveDependency)
	case ToolGetProjectInfo:
		return bridgeCall[toolingv1.GetProjectInfoRequest, toolingv1.GetProjectInfoResponse](ctx, req, b.inner.GetProjectInfo)
	case ToolGetSemanticIndex:
		return bridgeCall[toolingv1.GetSemanticIndexRequest, toolingv1.GetSemanticIndexResponse](ctx, req, b.inner.GetSemanticIndex)
	case ToolGetInstructionIndex:
		return bridgeCall[toolingv1.GetInstructionIndexRequest, toolingv1.GetInstructionIndexResponse](ctx, req, b.inner.GetInstructionIndex)
	case ToolBuild:
		return bridgeCall[toolingv1.BuildRequest, toolingv1.BuildResponse](ctx, req, b.inner.Build)
	case ToolTest:
		return bridgeCall[toolingv1.TestRequest, toolingv1.TestResponse](ctx, req, b.inner.Test)
	case ToolLint:
		return bridgeCall[toolingv1.LintRequest, toolingv1.LintResponse](ctx, req, b.inner.Lint)
	default:
		return &toolboxv1.CallToolResponse{
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
	call *toolboxv1.CallToolRequest,
	handler func(context.Context, ReqP) (RespP, error),
) (*toolboxv1.CallToolResponse, error) {
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
	return &toolboxv1.CallToolResponse{
		Content: []*toolboxv1.Content{
			{Body: &toolboxv1.Content_Structured{Structured: out}},
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

func errResp(format string, args ...any) *toolboxv1.CallToolResponse {
	return &toolboxv1.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

// --- typed Mind wrapper -----------------------------------------

// ToolingFromToolbox wraps a Toolbox client and presents the typed
// ToolingClient interface. Mind's existing call sites continue
// working: client.Test(ctx, req) routes to CallTool("lang.test", req)
// under the hood.
//
// Returns toolingv1.ToolingClient so Mind can drop this in as a
// type-compatible replacement for a real ToolingClient over the
// Tooling RPC. When Phase α deletes the Tooling proto, this wrapper
// stays — Mind keeps its typed interface; the underlying contract
// just stops being a separate proto.
func ToolingFromToolbox(c toolboxv1.ToolboxClient) toolingv1.ToolingClient {
	return &toolingFromToolbox{c: c}
}

type toolingFromToolbox struct {
	c toolboxv1.ToolboxClient
}

// callBridge sends a typed proto request through CallTool and
// decodes the typed proto response. Inverse of bridgeCall.
func callBridge[Req proto.Message, Resp proto.Message](
	ctx context.Context, c toolboxv1.ToolboxClient,
	tool string, req Req, resp Resp,
) (Resp, error) {
	args, err := protoToStruct(req)
	if err != nil {
		var zero Resp
		return zero, fmt.Errorf("encode %s: %w", tool, err)
	}
	out, err := c.CallTool(ctx, &toolboxv1.CallToolRequest{
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

func (t *toolingFromToolbox) Fix(ctx context.Context, in *toolingv1.FixRequest, _ ...grpc.CallOption) (*toolingv1.FixResponse, error) {
	return callBridge(ctx, t.c, ToolFix, in, &toolingv1.FixResponse{})
}
func (t *toolingFromToolbox) ApplyEdit(ctx context.Context, in *toolingv1.ApplyEditRequest, _ ...grpc.CallOption) (*toolingv1.ApplyEditResponse, error) {
	return callBridge(ctx, t.c, ToolApplyEdit, in, &toolingv1.ApplyEditResponse{})
}
func (t *toolingFromToolbox) ListDependencies(ctx context.Context, in *toolingv1.ListDependenciesRequest, _ ...grpc.CallOption) (*toolingv1.ListDependenciesResponse, error) {
	return callBridge(ctx, t.c, ToolListDependencies, in, &toolingv1.ListDependenciesResponse{})
}
func (t *toolingFromToolbox) AddDependency(ctx context.Context, in *toolingv1.AddDependencyRequest, _ ...grpc.CallOption) (*toolingv1.AddDependencyResponse, error) {
	return callBridge(ctx, t.c, ToolAddDependency, in, &toolingv1.AddDependencyResponse{})
}
func (t *toolingFromToolbox) RemoveDependency(ctx context.Context, in *toolingv1.RemoveDependencyRequest, _ ...grpc.CallOption) (*toolingv1.RemoveDependencyResponse, error) {
	return callBridge(ctx, t.c, ToolRemoveDependency, in, &toolingv1.RemoveDependencyResponse{})
}
func (t *toolingFromToolbox) GetProjectInfo(ctx context.Context, in *toolingv1.GetProjectInfoRequest, _ ...grpc.CallOption) (*toolingv1.GetProjectInfoResponse, error) {
	return callBridge(ctx, t.c, ToolGetProjectInfo, in, &toolingv1.GetProjectInfoResponse{})
}
func (t *toolingFromToolbox) GetSemanticIndex(ctx context.Context, in *toolingv1.GetSemanticIndexRequest, _ ...grpc.CallOption) (*toolingv1.GetSemanticIndexResponse, error) {
	return callBridge(ctx, t.c, ToolGetSemanticIndex, in, &toolingv1.GetSemanticIndexResponse{})
}
func (t *toolingFromToolbox) GetInstructionIndex(ctx context.Context, in *toolingv1.GetInstructionIndexRequest, _ ...grpc.CallOption) (*toolingv1.GetInstructionIndexResponse, error) {
	return callBridge(ctx, t.c, ToolGetInstructionIndex, in, &toolingv1.GetInstructionIndexResponse{})
}
func (t *toolingFromToolbox) Build(ctx context.Context, in *toolingv1.BuildRequest, _ ...grpc.CallOption) (*toolingv1.BuildResponse, error) {
	return callBridge(ctx, t.c, ToolBuild, in, &toolingv1.BuildResponse{})
}
func (t *toolingFromToolbox) Test(ctx context.Context, in *toolingv1.TestRequest, _ ...grpc.CallOption) (*toolingv1.TestResponse, error) {
	return callBridge(ctx, t.c, ToolTest, in, &toolingv1.TestResponse{})
}
func (t *toolingFromToolbox) Lint(ctx context.Context, in *toolingv1.LintRequest, _ ...grpc.CallOption) (*toolingv1.LintResponse, error) {
	return callBridge(ctx, t.c, ToolLint, in, &toolingv1.LintResponse{})
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

func schemaForTool(name string) *structpb.Struct {
	var schema map[string]any
	switch name {
	case ToolFix:
		schema = map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []any{"file"},
			"properties": map[string]any{
				"file":   map[string]any{"type": "string", "minLength": 1, "description": "Source-root-relative file path."},
				"mode":   map[string]any{"type": "string", "enum": []any{"FIX_MODE_SAFE", "FIX_MODE_NONE", "FIX_MODE_AGGRESSIVE"}, "default": "FIX_MODE_SAFE"},
				"dryRun": map[string]any{"type": "boolean", "default": false, "description": "Return content and hashes without writing."},
			},
		}
	case ToolApplyEdit:
		schema = map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []any{"file", "find", "replace"},
			"properties": map[string]any{
				"file":    map[string]any{"type": "string", "minLength": 1, "description": "Source-root-relative file path."},
				"find":    map[string]any{"type": "string"},
				"replace": map[string]any{"type": "string"},
				"fixMode": map[string]any{"type": "string", "enum": []any{"FIX_MODE_SAFE", "FIX_MODE_NONE", "FIX_MODE_AGGRESSIVE"}, "default": "FIX_MODE_SAFE"},
				"dryRun":  map[string]any{"type": "boolean", "default": false, "description": "Return content and hashes without writing."},
			},
		}
	default:
		return anyObjectSchema()
	}
	result, _ := structpb.NewStruct(schema)
	return result
}
