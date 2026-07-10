// Package linear implements the codefly toolbox contract for Linear via its
// GraphQL API. Linear responses are deeply nested and verbose; each tool runs
// the raw JSON through a gortk `json` Spec that flattens the issue array into
// one compact line per issue — the "gortk for API output" path (applied by
// spec, not by command routing).
//
// Auth: a Linear API key (https://linear.app/settings/api), passed to New
// (the cmd reads LINEAR_API_KEY).
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
	"github.com/codefly-dev/gortk"
)

const endpoint = "https://api.linear.app/graphql"
const maxBodyBytes = 4 << 20

// Common selection set: the fields worth surfacing per issue.
const issueFields = `nodes { identifier title url priorityLabel state { name } assignee { displayName } }`

// itemTemplate renders one issue line; shared by both tools.
const itemTemplate = "{identifier} [{state.name}] {title} — {assignee.displayName} ({priorityLabel}) {url}"

// Precompiled gortk filters: same per-issue shape, different array path
// (issues vs issueSearch). Applied directly to the GraphQL JSON.
var (
	issuesFilter = mustCompile("linear-issues", "data.issues.nodes")
	searchFilter = mustCompile("linear-search", "data.issueSearch.nodes")
)

func mustCompile(name, arrayField string) gortk.Filter {
	f, err := gortk.Spec{
		Name:  name,
		Match: gortk.MatchSpec{Command: "linear"}, // satisfies validation; we Apply directly
		JSON: &gortk.JSONSpec{
			ArrayField:      arrayField,
			ItemTemplate:    itemTemplate,
			SummaryTemplate: "linear: {count} issue(s)",
		},
	}.Compile()
	if err != nil {
		panic("linear toolbox: " + err.Error())
	}
	return f
}

// Server implements the Toolbox contract for Linear.
type Server struct {
	*registry.Base

	apiKey  string
	version string
	client  *http.Client
}

// New returns a Server using the given Linear API key.
func New(apiKey, version string) *Server {
	s := &Server{apiKey: apiKey, version: version, client: &http.Client{Timeout: 20 * time.Second}}
	s.Base = registry.NewBase(s)
	return s
}

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "linear",
		Version:        s.version,
		Description:    "Linear issues via GraphQL, compacted to one line per issue for LLM context.",
		CanonicalFor:   []string{"linear"},
		SandboxSummary: "network required (api.linear.app); reads Linear via LINEAR_API_KEY",
	}, nil
}

func (s *Server) Tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "linear.issues",
			SummaryDescription: "List recent Linear issues, one compact line each. Read-only.",
			LongDescription: "Fetches recent issues via Linear's GraphQL API and returns them compacted: " +
				"`IDENT [State] Title — Assignee (Priority) url`, one per line. Use `limit` to scope (default 25).",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": map[string]any{"limit": map[string]any{"type": "integer", "description": "Max issues. Default 25.", "minimum": 1, "maximum": 100}},
			}),
			Tags:        []string{"linear", "read-only", "network"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns `linear: ...` when LINEAR_API_KEY is missing/invalid or the API errors.",
			Examples: []*toolboxv0.ToolExample{
				{Description: "List the 10 most recent issues.", Arguments: mustStruct(map[string]any{"limit": 10}), ExpectedOutcome: "Up to 10 compact issue lines plus a count summary."},
			},
			Handler: s.issues,
		},
		{
			Name:               "linear.search",
			SummaryDescription: "Full-text search Linear issues, one compact line each. Read-only.",
			LongDescription:    "Searches issues via Linear's `issueSearch` and returns the same compact one-line-per-issue form. Requires `query`.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search text."},
					"limit": map[string]any{"type": "integer", "description": "Max results. Default 25.", "minimum": 1, "maximum": 100},
				},
				"required": []any{"query"},
			}),
			Tags:        []string{"linear", "read-only", "network"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns `linear: ...` on auth/API failure or when `query` is empty.",
			Examples: []*toolboxv0.ToolExample{
				{Description: "Find issues mentioning 'compression'.", Arguments: mustStruct(map[string]any{"query": "compression"}), ExpectedOutcome: "Compact issue lines matching the search."},
			},
			Handler: s.search,
		},
	}
}

// --- handlers ----------------------------------------------------------------

func (s *Server) issues(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	q := fmt.Sprintf(`query($first:Int!){issues(first:$first, orderBy: updatedAt){%s}}`, issueFields)
	body, err := s.graphql(ctx, q, map[string]any{"first": intArg(args, "limit", 25)})
	if err != nil {
		return respond.Error("linear: %v", err)
	}
	return respond.Text(issuesFilter.Apply(gortk.Command{Stdout: body}).Text)
}

func (s *Server) search(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	term, _ := args["query"].(string)
	if term == "" {
		return respond.Error("linear.search: `query` is required")
	}
	q := fmt.Sprintf(`query($q:String!,$first:Int!){issueSearch(query:$q, first:$first){%s}}`, issueFields)
	body, err := s.graphql(ctx, q, map[string]any{"q": term, "first": intArg(args, "limit", 25)})
	if err != nil {
		return respond.Error("linear: %v", err)
	}
	return respond.Text(searchFilter.Apply(gortk.Command{Stdout: body}).Text)
}

// graphql POSTs a query and returns the raw response body, surfacing transport,
// HTTP, and GraphQL-level errors.
func (s *Server) graphql(ctx context.Context, query string, vars map[string]any) ([]byte, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("LINEAR_API_KEY not set")
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", s.apiKey)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear API %d: %s", resp.StatusCode, string(b))
	}
	var probe struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(b, &probe); len(probe.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", probe.Errors[0].Message)
	}
	return b, nil
}

// --- helpers -----------------------------------------------------------------

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("linear toolbox: cannot encode example args: %v", err))
	}
	return s
}
