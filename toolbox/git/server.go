package git

import (
	"context"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/internal/registry"
	"github.com/codefly-dev/core/toolbox/internal/respond"
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

// tools is the single source of truth for this toolbox's callable
// surface. The three ListTools / ListToolSummaries / DescribeTool
// methods all project from this slice via the registry helpers.
//
// Authors editing tools here must keep:
//   - SummaryDescription (one line, ≤120 chars) — drives routing
//   - LongDescription (multi-paragraph OK) — drives the per-call
//     spec; spell out edge cases and when not to use
//   - Examples (≥1) — LLMs do dramatically better with examples
//     than with schemas alone; this is the entire point of
//     fetching ToolSpec on-demand
//   - Tags — at minimum the toolbox name + a read-only/destructive
//     marker; add domain tags to help pre-filtering
func (s *Server) tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "git.status",
			SummaryDescription: "Working-tree status (clean? dirty? which files?). Read-only.",
			LongDescription: "Returns the working-tree status as a structured object: a `clean` boolean " +
				"and a `files` map of path → two-character marker (staging + worktree status, mirroring " +
				"`git status --short`). Use to detect uncommitted changes before invoking a destructive " +
				"operation, or to scope an LLM's edits.\n\n" +
				"Operates on the workspace the toolbox was constructed with — multi-repo workspaces " +
				"need one toolbox per repo.",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}),
			Tags:        []string{"git", "read-only", "filesystem"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns error envelope with `open repo: ...` when the workspace isn't a git repository, or `worktree: ...` when the index is corrupted.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Check if the working tree is clean before staging an edit.",
					Arguments:       mustStruct(map[string]any{}),
					ExpectedOutcome: "{ clean: true, files: {} } when nothing changed since the last commit; otherwise files map shows pending paths.",
				},
			},
		},
		{
			Name:               "git.log",
			SummaryDescription: "Recent commits on HEAD (or a ref). Read-only. Multi-line messages may be truncated by --short.",
			LongDescription: "Lists commits in reverse-chronological order from HEAD (or a specified ref). " +
				"Each commit returns hash, author name, and message. Messages can be multi-line and " +
				"arbitrarily long; pass `short=true` to receive only the subject line. Use `limit` to cap " +
				"the response — default 20 — when surfacing into an LLM context.\n\n" +
				"Currently always reads HEAD regardless of `ref`; the ref parameter is reserved for a " +
				"later iteration that needs to disambiguate branch vs. tag vs. commit hash.",
			InputSchema: respond.Schema(map[string]any{
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
					"short": map[string]any{
						"type":        "boolean",
						"description": "Return only the first line of each commit message (subject). Default false.",
					},
				},
			}),
			Tags:        []string{"git", "read-only", "filesystem"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns `log: ...` when the repository has no commits, or `open repo: ...` when not a git repo.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Get the last 5 commits with full message bodies.",
					Arguments:       mustStruct(map[string]any{"limit": 5}),
					ExpectedOutcome: "Array of up to 5 commit objects, each with hash + author + message. Fewer if the repo has fewer commits.",
				},
				{
					Description:     "Show 20 subject lines for a quick history overview.",
					Arguments:       mustStruct(map[string]any{"limit": 20, "short": true}),
					ExpectedOutcome: "Up to 20 commits, message field is the subject only (no body).",
				},
			},
		},
		{
			Name:               "git.diff",
			SummaryDescription: "Diff between two refs or HEAD vs working tree. Read-only. Phase 1 stub — currently returns 'not implemented'.",
			LongDescription: "Will return a unified diff between two refs (or HEAD vs the working tree). " +
				"Phase 1 of the git toolbox does not implement this — `git.status` + `git.log` cover the " +
				"common read-only flows. Calling git.diff today returns an actionable error so the agent " +
				"can fall back; the dispatch case is in place so a later commit only swaps the body.",
			InputSchema: respond.Schema(map[string]any{
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
			Tags:        []string{"git", "read-only", "filesystem", "stub"},
			Idempotency: "idempotent",
			ErrorModes:  "Always returns 'git.diff not yet implemented; status + log are usable today' until the body lands.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Diff working tree against HEAD (no args = current changes).",
					Arguments:       mustStruct(map[string]any{}),
					ExpectedOutcome: "Currently returns the not-implemented error; will return unified diff text once the body lands.",
				},
			},
		},
	}
}

// ListTools (legacy heavy envelope) — projects tools() to the
// flat *toolboxv0.Tool[] shape. Kept for transitional consumers
// and for MCP transcoding. New code prefers ListToolSummaries +
// DescribeTool.
func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{Tools: registry.AsTools(s.tools())}, nil
}

// ListToolSummaries (lightweight catalog) — the routing surface.
// Optional tags_filter pre-selects e.g. ["read-only"] subsets.
func (s *Server) ListToolSummaries(_ context.Context, req *toolboxv0.ListToolSummariesRequest) (*toolboxv0.ListToolSummariesResponse, error) {
	return &toolboxv0.ListToolSummariesResponse{
		Tools: registry.AsSummaries(s.tools(), req.GetTagsFilter()),
	}, nil
}

// DescribeTool (full per-tool spec) — fetched on-demand right
// before CallTool. Returns DescribeToolResponse.error when the
// requested name doesn't exist.
func (s *Server) DescribeTool(_ context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	spec := registry.FindSpec(s.tools(), req.GetName())
	if spec == nil {
		return &toolboxv0.DescribeToolResponse{
			Error: fmt.Sprintf("unknown tool %q (call ListToolSummaries to enumerate)", req.GetName()),
		}, nil
	}
	return &toolboxv0.DescribeToolResponse{Tool: spec}, nil
}

// mustStruct is a small helper for the inline tools() definitions
// — the structpb.NewStruct error path can't fire on literal
// map[string]any inputs, but we still wrap to keep the call sites
// short.
func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("git toolbox: cannot encode example args: %v", err))
	}
	return s
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
		return respond.Error("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) status(_ context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return respond.Error("open repo: %v", err), nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return respond.Error("worktree: %v", err), nil
	}
	st, err := wt.Status()
	if err != nil {
		return respond.Error("status: %v", err), nil
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
	return respond.Struct(payload), nil
}

func (s *Server) log(_ context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	short := false
	if v, ok := args["short"].(bool); ok {
		short = v
	}

	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return respond.Error("open repo: %v", err), nil
	}

	// `ref` defaults to HEAD; specifying a non-default ref is left
	// for a later iteration (need to disambiguate branch vs tag vs
	// commit hash).
	logIter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		return respond.Error("log: %v", err), nil
	}
	defer logIter.Close()

	commits := make([]any, 0, limit)
	for count := 0; count < limit; count++ {
		c, err := logIter.Next()
		if err != nil {
			// io.EOF or any walker exhaustion — done.
			break
		}
		message := c.Message
		if short {
			// First line only — git's --oneline equivalent. Defends
			// against multi-paragraph commit bodies blowing out the
			// agent's context budget.
			if i := indexNewline(message); i >= 0 {
				message = message[:i]
			}
		}
		commits = append(commits, map[string]any{
			"hash":    c.Hash.String(),
			"author":  c.Author.Name,
			"message": message,
		})
	}
	return respond.Struct(map[string]any{"commits": commits}), nil
}

// indexNewline returns the index of the first '\n' in s, or -1 if
// none. Inlined to avoid pulling strings just for this.
func indexNewline(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}

func (s *Server) diff(_ context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	// Phase 1 stub: full diff between two refs needs object-tree
	// walking (Patch, Diff). Surface a clear "not implemented" so the
	// agent knows to fall back; tests cover the dispatch path.
	return respond.Error("git.diff not yet implemented; status + log are usable today"), nil
}
