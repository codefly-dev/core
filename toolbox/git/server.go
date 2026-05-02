package git

import (
	"context"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/respond"
	"github.com/codefly-dev/core/toolbox/registry"
)

// Server implements the codefly.services.toolbox.v0.Toolbox contract
// for git operations against a single workspace directory.
//
// Embeds *registry.Base for the four boilerplate RPCs (ListTools,
// ListToolSummaries, DescribeTool, CallTool); the plugin only owns
// Identity() + Tools() and the per-tool handler methods.
//
// Construct with the workspace root; every Tool dispatches against
// that directory. Multi-repo workspaces would either need one Server
// per repo (the simple route) or a Roots-based tool argument that
// scopes per-call (the MCP-shape route, deferred until we have the
// multi-repo use case in front of us).
type Server struct {
	*registry.Base

	workspace string
	version   string
}

// New returns a Server bound to the given workspace directory.
// workspace must be an existing directory that contains a `.git`
// entry — validation defers to the first git operation that needs
// to open the repo, which surfaces a clear go-git error.
func New(workspace, version string) *Server {
	s := &Server{workspace: workspace, version: version}
	s.Base = registry.NewBase(s)
	return s
}

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:         "git",
		Version:      s.version,
		Description:  "Git repository operations as typed RPCs (status, log, diff, ...).",
		CanonicalFor: []string{"git"},
		SandboxSummary: fmt.Sprintf(
			"reads+writes %s; network deny (push/pull need explicit grant)",
			s.workspace),
	}, nil
}

// Tools is the single source of truth for this toolbox's callable
// surface. The four RPCs (ListTools / ListToolSummaries /
// DescribeTool / CallTool) all project from this slice via
// registry.Base.
//
// Authors editing tools here must keep:
//   - SummaryDescription (one line, ≤120 chars) — drives routing
//   - LongDescription (multi-paragraph OK) — drives the per-call
//     spec; spell out edge cases and when not to use
//   - Examples (≥1) — LLMs do dramatically better with examples
//     than with schemas alone
//   - Tags — at minimum the toolbox name + a read-only/destructive
//     marker; add domain tags to help pre-filtering
//   - Handler — the implementation. Base.CallTool dispatches here.
func (s *Server) Tools() []*registry.ToolDefinition {
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
			Handler: s.status,
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
			Handler: s.log,
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
			Handler: s.diff,
		},
	}
}

// mustStruct is a small helper for the inline Tools() definitions —
// the structpb.NewStruct error path can't fire on literal
// map[string]any inputs, but we still wrap to keep call sites short.
func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("git toolbox: cannot encode example args: %v", err))
	}
	return s
}

// --- Tool implementations ----------------------------------------

func (s *Server) status(_ context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return respond.Error("open repo: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return respond.Error("worktree: %v", err)
	}
	st, err := wt.Status()
	if err != nil {
		return respond.Error("status: %v", err)
	}

	// Compact representation: file → "MM " (staged + worktree status).
	files := make(map[string]any, len(st))
	for path, fs := range st {
		files[path] = fmt.Sprintf("%c%c", fs.Staging, fs.Worktree)
	}
	return respond.Struct(map[string]any{
		"clean": st.IsClean(),
		"files": files,
	})
}

func (s *Server) log(_ context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
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
		return respond.Error("open repo: %v", err)
	}

	// `ref` defaults to HEAD; specifying a non-default ref is left
	// for a later iteration (need to disambiguate branch vs tag vs
	// commit hash).
	logIter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		return respond.Error("log: %v", err)
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
	return respond.Struct(map[string]any{"commits": commits})
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

func (s *Server) diff(_ context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	// Phase 1 stub: full diff between two refs needs object-tree
	// walking (Patch, Diff). Surface a clear "not implemented" so the
	// agent knows to fall back; tests cover the dispatch path.
	return respond.Error("git.diff not yet implemented; status + log are usable today")
}
