// Package registry holds the shared "tool definition" type each
// codefly toolbox uses to declare its callable surface ONCE and
// project to whichever response shape the host requested:
//
//   - Legacy *toolboxv0.Tool        — heavy, returned by ListTools
//   - *toolboxv0.ToolSummary        — light, returned by ListToolSummaries
//   - *toolboxv0.ToolSpec           — heavy + examples, returned by DescribeTool
//
// Toolboxes used to keep tool definitions inline in their ListTools
// method bodies and re-derive Tool messages on every call. With the
// two-phase contract in place we need the SAME tool data shaped
// three different ways. The registry keeps the source of truth in
// one place.
//
// Go-SDK convenience for codefly toolbox plugin authors. The wire
// contract is the Toolbox gRPC service in proto/codefly/services/
// toolbox/v0; this package is just the ergonomic Go shape on top.
// Plugin authors writing in other languages implement the proto
// directly. This package is NOT a stability promise to third-party
// Go plugins — treat it as semi-public, may evolve with the
// framework.
package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	coretoolbox "github.com/codefly-dev/core/toolbox"
)

// ToolDefinition is the single source of truth for one tool's
// metadata. Toolboxes hold a []ToolDefinition; helpers project to
// the three proto envelopes.
//
// Field semantics mirror the proto messages — see
// proto/codefly/services/toolbox/v0/toolbox.proto for canonical
// docs. Brief notes here on the dual-description split:
type ToolDefinition struct {
	// Name is the dotted tool name (e.g. "git.status").
	Name string

	// SummaryDescription is the ONE-LINE description used in
	// ToolSummary — optimized for "should I pick this?".
	// Required; ~120 chars or fewer by convention.
	SummaryDescription string

	// LongDescription is the multi-paragraph description used in
	// ToolSpec — explain the contract, edge cases, when not to
	// use. Optional; if empty, falls back to SummaryDescription.
	LongDescription string

	// InputSchema is the JSON Schema for arguments. Encoded as a
	// structpb so we don't mirror JSON Schema's recursive shape
	// in proto.
	InputSchema *structpb.Struct

	// OutputSchema is optional — set when the toolbox wants to
	// constrain its output shape. Most tools leave this nil.
	OutputSchema *structpb.Struct

	// Destructive marks state-mutating tools. Hosts surface extra
	// confirmation UI for these.
	Destructive bool

	// Tags drive pre-filtering in routing (read-only, network,
	// filesystem, destructive, plus the toolbox name + free-form
	// domain tags). The toolbox name is auto-prepended by helpers
	// IF not present, so authors don't need to repeat it.
	Tags []string

	// Examples are worked invocations. AT LEAST ONE per tool by
	// convention — LLMs structure args dramatically better with
	// examples than with schemas alone. This is the load-bearing
	// addition of the two-phase design.
	Examples []*toolboxv0.ToolExample

	// Idempotency: "idempotent" | "side_effecting" | "" (unknown).
	Idempotency string

	// ErrorModes is free-text on what failure looks like and what
	// the caller should do. Helps the LLM diagnose without re-
	// running the call.
	ErrorModes string

	// Handler is the implementation invoked by Base.CallTool when
	// req.Name matches d.Name. Optional — if nil, Base.CallTool
	// returns an "unimplemented" error for that tool. Toolboxes that
	// dispatch via their own switch (legacy pattern) leave this nil.
	//
	// The handler is responsible for argument extraction, validation,
	// and response shaping. It must NOT panic; on internal error
	// return a CallToolResponse with the error string set.
	Handler func(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse
}

// ToTool returns the legacy heavy envelope used by ListTools.
func (d *ToolDefinition) ToTool() *toolboxv0.Tool {
	desc := d.LongDescription
	if desc == "" {
		desc = d.SummaryDescription
	}
	return &toolboxv0.Tool{
		Name:         d.Name,
		Description:  desc,
		InputSchema:  d.InputSchema,
		OutputSchema: d.OutputSchema,
		Destructive:  d.Destructive,
	}
}

// ToSummary returns the lightweight catalog entry used by
// ListToolSummaries.
func (d *ToolDefinition) ToSummary() *toolboxv0.ToolSummary {
	return &toolboxv0.ToolSummary{
		Name:        d.Name,
		Description: d.SummaryDescription,
		Tags:        d.Tags,
		Destructive: d.Destructive,
	}
}

// ToSpec returns the full spec returned by DescribeTool.
func (d *ToolDefinition) ToSpec() *toolboxv0.ToolSpec {
	desc := d.LongDescription
	if desc == "" {
		desc = d.SummaryDescription
	}
	return &toolboxv0.ToolSpec{
		Name:         d.Name,
		Description:  desc,
		InputSchema:  d.InputSchema,
		OutputSchema: d.OutputSchema,
		Destructive:  d.Destructive,
		Tags:         d.Tags,
		Examples:     d.Examples,
		Idempotency:  d.Idempotency,
		ErrorModes:   d.ErrorModes,
	}
}

// AsTools projects every definition into the legacy envelope.
func AsTools(defs []*ToolDefinition) []*toolboxv0.Tool {
	out := make([]*toolboxv0.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.ToTool())
	}
	return out
}

// AsSummaries projects every definition into the lightweight
// catalog. Optionally pre-filters by tags — every tag in
// tagsFilter must be present on the definition's tags (AND
// semantics). Empty filter returns everything.
func AsSummaries(defs []*ToolDefinition, tagsFilter []string) []*toolboxv0.ToolSummary {
	out := make([]*toolboxv0.ToolSummary, 0, len(defs))
	for _, d := range defs {
		if !hasAllTags(d.Tags, tagsFilter) {
			continue
		}
		out = append(out, d.ToSummary())
	}
	return out
}

// FindSpec returns the spec for the named tool, or nil when the name
// doesn't exist. Caller decides whether to surface as an error.
func FindSpec(defs []*ToolDefinition, name string) *toolboxv0.ToolSpec {
	for _, d := range defs {
		if d.Name == name {
			return d.ToSpec()
		}
	}
	return nil
}

// Descriptor is the static identity of a toolbox plugin. Passing it to
// NewBase gives the plugin a complete Identity RPC and automatically adds the
// toolbox name to every tool's tags.
type Descriptor struct {
	Name           string
	Version        string
	Description    string
	CanonicalFor   []string
	SandboxSummary string
}

// Base is an embeddable that provides every Toolbox RPC. A plugin declares its
// descriptor and tool definitions once; Base owns identity, catalog projection,
// schema validation, and dispatch.
//
// Usage:
//
//	type Server struct {
//	    *registry.Base
//	    // ... plugin-specific fields
//	}
//
//	func New(version string) *Server {
//	    s := &Server{}
//	    s.Base = registry.NewBase(registry.Descriptor{Name: "example", Version: version}, s.Tools()...)
//	    return s
//	}
type Base struct {
	toolboxv0.UnimplementedToolboxServer

	mu          sync.RWMutex
	descriptor  Descriptor
	definitions []*ToolDefinition
	tools       []*ToolDefinition
}

// NewBase constructs the complete shared toolbox implementation. Descriptor is
// required; there is no partially-wired mode.
func NewBase(descriptor Descriptor, definitions ...*ToolDefinition) *Base {
	validateDescriptor(descriptor)
	b := &Base{descriptor: descriptor}
	b.setTools(definitions)
	return b
}

// SetDescriptor replaces a dynamically discovered identity. It is primarily
// used by protocol adapters such as mcprev after their initialization
// handshake. Native plugins should supply the final descriptor to NewBase.
func (b *Base) SetDescriptor(descriptor Descriptor) {
	validateDescriptor(descriptor)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.descriptor = descriptor
	b.tools = normalizeDefinitions(b.definitions, descriptor.Name)
}

// SetTools replaces the declared tool surface atomically. Native plugins set
// tools in NewBase; dynamic protocol adapters use this after discovery.
func (b *Base) SetTools(definitions ...*ToolDefinition) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setTools(definitions)
}

func (b *Base) setTools(definitions []*ToolDefinition) {
	b.definitions = append([]*ToolDefinition(nil), definitions...)
	b.tools = normalizeDefinitions(definitions, b.descriptor.Name)
}

func validateDescriptor(descriptor Descriptor) {
	if strings.TrimSpace(descriptor.Name) == "" {
		panic("registry.NewBase: descriptor name is required")
	}
}

// Identity returns the descriptor supplied at construction.
func (b *Base) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return &toolboxv0.IdentityResponse{
		Name:           b.descriptor.Name,
		Version:        b.descriptor.Version,
		Description:    b.descriptor.Description,
		CanonicalFor:   append([]string(nil), b.descriptor.CanonicalFor...),
		SandboxSummary: b.descriptor.SandboxSummary,
	}, nil
}

func (b *Base) snapshotTools() []*ToolDefinition {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]*ToolDefinition(nil), b.tools...)
}

func normalizeDefinitions(definitions []*ToolDefinition, toolboxName string) []*ToolDefinition {
	tools := make([]*ToolDefinition, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for index, definition := range definitions {
		if definition == nil {
			panic(fmt.Sprintf("registry.NewBase: tool definition %d is nil", index))
		}
		if strings.TrimSpace(definition.Name) == "" {
			panic(fmt.Sprintf("registry.NewBase: tool definition %d has no name", index))
		}
		if _, exists := seen[definition.Name]; exists {
			panic(fmt.Sprintf("registry.NewBase: duplicate tool %q", definition.Name))
		}
		seen[definition.Name] = struct{}{}

		copy := *definition
		copy.Tags = append([]string(nil), definition.Tags...)
		if toolboxName != "" && !contains(copy.Tags, toolboxName) {
			copy.Tags = append([]string{toolboxName}, copy.Tags...)
		}
		tools = append(tools, &copy)
	}
	return tools
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// ListTools (legacy) — returns the heavy envelope for every tool.
func (b *Base) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{Tools: AsTools(b.snapshotTools())}, nil
}

// ListToolSummaries (catalog) — lightweight, with optional tags
// filter. AND semantics: every tag in tags_filter must be present.
func (b *Base) ListToolSummaries(_ context.Context, req *toolboxv0.ListToolSummariesRequest) (*toolboxv0.ListToolSummariesResponse, error) {
	return &toolboxv0.ListToolSummariesResponse{
		Tools: AsSummaries(b.snapshotTools(), req.GetTagsFilter()),
	}, nil
}

// DescribeTool (full spec) — returns ToolSpec for the named tool, or
// an error string in the response when the name is unknown. We
// return the error in-band rather than as a gRPC error because the
// LLM caller benefits from a human-readable hint.
func (b *Base) DescribeTool(_ context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	spec := FindSpec(b.snapshotTools(), req.GetName())
	if spec == nil {
		return &toolboxv0.DescribeToolResponse{
			Error: fmt.Sprintf("unknown tool %q (call ListToolSummaries to enumerate)", req.GetName()),
		}, nil
	}
	return &toolboxv0.DescribeToolResponse{Tool: spec}, nil
}

// CallTool dispatches by name to the matching ToolDefinition.Handler.
// Unknown name → in-band error response. Tool with nil Handler →
// "unimplemented" error response (developer mistake; surface clearly
// rather than panic).
//
// **Schema validation.** Every tool must declare InputSchema and arguments are
// validated against it BEFORE the handler runs. The
// LLM-attack-surface is the args map: every plugin handler used to
// have to type-assert and nil-check by hand, with the failure mode
// being either a bad response or a runtime panic in the plugin
// process. Centralizing here means one validator for every plugin,
// fail-closed by default, with a clear in-band error so the model
// can repair its call.
//
// A missing or malformed schema fails closed as a plugin contract bug.
func (b *Base) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	for _, d := range b.snapshotTools() {
		if d.Name != req.GetName() {
			continue
		}
		if d.Handler == nil {
			return &toolboxv0.CallToolResponse{
				Error: fmt.Sprintf("tool %q has no handler — toolbox plugin bug", d.Name),
			}, nil
		}
		if err := coretoolbox.ValidateArguments(d.InputSchema, req.GetArguments()); err != nil {
			return &toolboxv0.CallToolResponse{
				Error: fmt.Sprintf("invalid arguments for %q: %s", d.Name, err),
			}, nil
		}
		return d.Handler(ctx, req), nil
	}
	return &toolboxv0.CallToolResponse{
		Error: fmt.Sprintf("unknown tool %q (call ListToolSummaries to enumerate)", req.GetName()),
	}, nil
}

// hasAllTags returns true when toolTags includes every entry in
// required. Empty required returns true (no filter). Linear scan
// — tag lists are tiny in practice.
func hasAllTags(toolTags, required []string) bool {
	for _, want := range required {
		found := false
		for _, got := range toolTags {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
