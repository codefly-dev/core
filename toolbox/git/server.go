package git

import (
	"context"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

// Server implements the codefly.services.toolbox.v0.Toolbox contract
// for git operations against a single workspace directory.
//
// Construct with the workspace root; every Tool dispatches against
// that directory. Multi-repo workspaces would either need one Server
// per repo (the simple route) or a Roots-based tool argument that
// scopes per-call (the MCP-shape route, deferred until we have the
// multi-repo use case in front of us).
type Server struct {
	toolboxv0.UnimplementedToolboxServer

	// workspace is the absolute path to the git working tree this
	// toolbox operates on. Set at construction; immutable for the
	// server's lifetime.
	workspace string

	// version is the toolbox version, surfaced in Identity.
	version string
}

// New returns a Server bound to the given workspace directory.
// workspace must be an existing directory that contains a `.git`
// entry — validation defers to the first git operation that needs
// to open the repo, which surfaces a clear go-git error.
func New(workspace, version string) *Server {
	return &Server{workspace: workspace, version: version}
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:        "git",
		Version:     s.version,
		Description: "Git repository operations as typed RPCs (status, log, diff, ...).",
		CanonicalFor: []string{"git"},
		SandboxSummary: fmt.Sprintf(
			"reads+writes %s; network deny (push/pull need explicit grant)",
			s.workspace),
	}, nil
}

// --- Tools -------------------------------------------------------

// ListTools enumerates every tool this server exposes, with JSON
// Schemas for arguments. Schemas are inline (not loaded from disk)
// so the contract is self-describing — no separate schema deploy.
func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	tools := []*toolboxv0.Tool{
		{
			Name:        "git.status",
			Description: "Return the working-tree status (clean? dirty? which files?).",
			InputSchema: mustSchema(map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}),
			Destructive: false,
		},
		{
			Name:        "git.log",
			Description: "List recent commits on HEAD (or a specified ref).",
			InputSchema: mustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum commits to return. Default 20.",
						"minimum":     1,
						"maximum":     1000,
					},
					"ref": map[string]any{
						"type":        "string",
						"description": "Branch or commit ref. Default HEAD.",
					},
				},
			}),
			Destructive: false,
		},
		{
			Name:        "git.diff",
			Description: "Diff between HEAD and the working tree, or between two refs.",
			InputSchema: mustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{
						"type":        "string",
						"description": "Source ref. Default HEAD.",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "Target ref. Default working tree (uncommitted changes).",
					},
				},
			}),
			Destructive: false,
		},
	}
	return &toolboxv0.ListToolsResponse{Tools: tools}, nil
}

// CallTool dispatches by name. Unknown tools surface as a non-routed
// error; canonical-routing isn't relevant within a toolbox itself
// (the bash toolbox is the canonical-routing layer).
func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "git.status":
		return s.status(ctx, req)
	case "git.log":
		return s.log(ctx, req)
	case "git.diff":
		return s.diff(ctx, req)
	default:
		return &toolboxv0.CallToolResponse{
			Error: fmt.Sprintf("unknown tool %q (call ListTools to enumerate)", req.Name),
		}, nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) status(_ context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return errResp("open repo: %v", err), nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return errResp("worktree: %v", err), nil
	}
	st, err := wt.Status()
	if err != nil {
		return errResp("status: %v", err), nil
	}

	// Compact representation: file → "MM " (staged + worktree status).
	files := make(map[string]any, len(st))
	for path, fs := range st {
		files[path] = fmt.Sprintf("%c%c", fs.Staging, fs.Worktree)
	}
	payload := map[string]any{
		"clean": st.IsClean(),
		"files": files,
	}
	return structResp(payload), nil
}

func (s *Server) log(_ context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := argMap(req)
	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return errResp("open repo: %v", err), nil
	}

	// `ref` defaults to HEAD; specifying a non-default ref is left
	// for a later iteration (need to disambiguate branch vs tag vs
	// commit hash).
	logIter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		return errResp("log: %v", err), nil
	}
	defer logIter.Close()

	commits := make([]any, 0, limit)
	for count := 0; count < limit; count++ {
		c, err := logIter.Next()
		if err != nil {
			// io.EOF or any walker exhaustion — done.
			break
		}
		commits = append(commits, map[string]any{
			"hash":    c.Hash.String(),
			"author":  c.Author.Name,
			"message": c.Message,
		})
	}
	return structResp(map[string]any{"commits": commits}), nil
}

func (s *Server) diff(_ context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	// Phase 1 stub: full diff between two refs needs object-tree
	// walking (Patch, Diff). Surface a clear "not implemented" so the
	// agent knows to fall back; tests cover the dispatch path.
	return errResp("git.diff not yet implemented; status + log are usable today"), nil
}

// --- Helpers -----------------------------------------------------

// argMap extracts the structured arguments from a CallToolRequest as
// a Go map. Returns an empty map when no arguments are provided —
// callers should treat missing keys as "use default."
func argMap(req *toolboxv0.CallToolRequest) map[string]any {
	if req.Arguments == nil {
		return map[string]any{}
	}
	return req.Arguments.AsMap()
}

// errResp wraps a non-routed error as a CallToolResponse. The
// canonical_routed flag stays false — git tool errors are git's
// problem (open failed, ref unknown, etc.), not a routing decision.
func errResp(format string, args ...any) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

// structResp wraps a Go map as a single structured Content block.
// Returns an error response if the map can't be marshaled to a
// proto Struct (a programmer error — we'd be passing non-JSON-able
// values like channels or functions).
func structResp(payload map[string]any) *toolboxv0.CallToolResponse {
	s, err := structpb.NewStruct(payload)
	if err != nil {
		return errResp("internal: cannot marshal response: %v", err)
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Structured{Structured: s}},
		},
	}
}

// mustSchema converts a JSON Schema-shaped Go map to a Struct. Used
// at server construction to bake schemas into Tool definitions; a
// failure here is a programmer typo and should panic so it surfaces
// in tests rather than runtime.
func mustSchema(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("bad input schema: %v", err))
	}
	return s
}
