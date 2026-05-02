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
// Internal — toolboxes import this; external callers use the proto
// types directly.
package registry

import (
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
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
