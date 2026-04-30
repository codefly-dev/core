package policyguard

import (
	"context"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
)

// Guard wraps a ToolboxServer with PDP enforcement. Identity /
// ListTools / list-style RPCs pass through unmolested — those are
// "what does this toolbox claim it can do" and don't mutate state.
// CallTool, ReadResource, and GetPrompt pass through the PDP
// because they're side-effecting (or at least information-disclosing).
type Guard struct {
	toolboxv0.UnimplementedToolboxServer

	inner   toolboxv0.ToolboxServer
	pdp     policy.PDP
	toolbox string // identity surfaced in PDPRequest.Toolbox

	// identity is the constant attribution for calls passing through
	// this guard. Hosts that want per-call attribution should run
	// per-caller guards (one per agent identity) rather than try to
	// thread context attribution through the gRPC layer.
	identity map[string]any
}

// New wraps inner. If pdp is nil, an AllowAllPDP is substituted —
// the wrapper is then a no-op. This makes Guard always-safe-to-
// install: code paths that haven't migrated their config to a real
// PDP get the same behavior they had before.
func New(inner toolboxv0.ToolboxServer, pdp policy.PDP, toolboxName string) *Guard {
	if pdp == nil {
		pdp = policy.AllowAllPDP{}
	}
	return &Guard{
		inner:   inner,
		pdp:     pdp,
		toolbox: toolboxName,
	}
}

// WithIdentity attaches a constant attribution map to every PDP
// request this Guard makes. Useful when the host knows it's wrapping
// a toolbox for a specific agent or session.
func (g *Guard) WithIdentity(identity map[string]any) *Guard {
	g.identity = identity
	return g
}

// --- Pass-through (no PDP) ---------------------------------------

func (g *Guard) Identity(ctx context.Context, req *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return g.inner.Identity(ctx, req)
}

func (g *Guard) ListTools(ctx context.Context, req *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return g.inner.ListTools(ctx, req)
}

func (g *Guard) ListResources(ctx context.Context, req *toolboxv0.ListResourcesRequest) (*toolboxv0.ListResourcesResponse, error) {
	return g.inner.ListResources(ctx, req)
}

func (g *Guard) ListPrompts(ctx context.Context, req *toolboxv0.ListPromptsRequest) (*toolboxv0.ListPromptsResponse, error) {
	return g.inner.ListPrompts(ctx, req)
}

// --- Policy-gated --------------------------------------------------

// CallTool is the load-bearing PDP gate. Refused calls return a
// CallToolResponse with the deny reason in Error — the SAME envelope
// a tool that refused itself would produce. The model sees an
// actionable refusal, not a transport error or a panic-style
// disconnect.
func (g *Guard) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetName(),
		Identity: g.identity,
	}
	if req.GetArguments() != nil {
		pdpReq.Args = req.GetArguments().AsMap()
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		return &toolboxv0.CallToolResponse{Error: d.Reason}, nil
	}
	return g.inner.CallTool(ctx, req)
}

// ReadResource is policy-gated for the same reason CallTool is —
// the resource URI may name a sensitive file the operator wants to
// keep out of agent context. PDP key for ReadResource: Tool is the
// URI string. Rules can match on the URI prefix via the
// suffix-match shorthand (e.g. Tool="config.yaml" matches any URI
// ending in /config.yaml).
func (g *Guard) ReadResource(ctx context.Context, req *toolboxv0.ReadResourceRequest) (*toolboxv0.ReadResourceResponse, error) {
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetUri(),
		Identity: g.identity,
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		return &toolboxv0.ReadResourceResponse{
			Content: []*toolboxv0.Content{{Body: &toolboxv0.Content_Text{Text: "policy: " + d.Reason}}},
		}, nil
	}
	return g.inner.ReadResource(ctx, req)
}

// GetPrompt is also gated — prompts may template in private context
// (paths, secrets) that the operator wants to deny per caller.
func (g *Guard) GetPrompt(ctx context.Context, req *toolboxv0.GetPromptRequest) (*toolboxv0.GetPromptResponse, error) {
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetName(),
		Identity: g.identity,
	}
	if req.GetArguments() != nil {
		pdpReq.Args = req.GetArguments().AsMap()
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		return &toolboxv0.GetPromptResponse{Description: "denied: " + d.Reason}, nil
	}
	return g.inner.GetPrompt(ctx, req)
}
