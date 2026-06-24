// Package github implements the codefly toolbox contract for GitHub — pure Go
// via the google/go-github SDK (no `gh` binary, matching the toolbox convention
// that git/docker/web are all SDK clients, not CLI shell-outs).
//
// owner/repo are derived from the workspace's `origin` remote (go-git), and can
// be overridden per call. Auth is a GitHub token (GITHUB_TOKEN), added via a
// transport — no oauth2 dependency.
//
// gortk does the compaction: PR/issue lists are normalized to a small JSON
// array and flattened to one line each by a gortk `json` Spec; a PR body is run
// through the gortk "gh" line filter to strip badge rows, images, and HTML
// comments.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gh "github.com/google/go-github/v37/github"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/llmout"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
	"github.com/mind-build/gortk"
)

// Precompiled gortk filters that flatten a normalized JSON array (one object per
// PR / issue / run) into one compact line each. Applied directly to JSON we
// build from the SDK structs.
var (
	prFilter    = mustCompile("github-prs", "#{number} [{state}] {title} — @{author} ({branch})")
	issueFilter = mustCompile("github-issues", "#{number} [{state}] {title} — @{author}")
	runFilter   = mustCompile("github-runs", "{id} {status}/{conclusion} {name} ({branch})")
)

func mustCompile(name, item string) gortk.Filter {
	f, err := gortk.Spec{
		Name:  name,
		Match: gortk.MatchSpec{Command: "github"}, // satisfies validation; applied directly
		JSON: &gortk.JSONSpec{
			ArrayField:      "",
			ItemTemplate:    item,
			SummaryTemplate: "github: {count} item(s)",
		},
	}.Compile()
	if err != nil {
		panic("github toolbox: " + err.Error())
	}
	return f
}

// Server implements the Toolbox contract for GitHub.
type Server struct {
	*registry.Base

	workspace string
	version   string
	client    *gh.Client
}

// New returns a Server using the given GitHub token, scoped to a workspace whose
// origin remote names the default owner/repo.
func New(workspace, token, version string) *Server {
	httpClient := &http.Client{Transport: tokenTransport{token: token, base: http.DefaultTransport}}
	s := &Server{
		workspace: workspace,
		version:   version,
		client:    gh.NewClient(httpClient),
	}
	s.Base = registry.NewBase(s)
	return s
}

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "github",
		Version:        s.version,
		Description:    "GitHub PRs, issues, and CI runs via the go-github SDK; output compacted for LLM context.",
		CanonicalFor:   []string{"github"},
		SandboxSummary: "network required (api.github.com); reads GitHub via GITHUB_TOKEN; owner/repo from origin remote",
	}, nil
}

func (s *Server) Tools() []*registry.ToolDefinition {
	repoProps := map[string]any{
		"owner": map[string]any{"type": "string", "description": "Repo owner. Default: from origin remote."},
		"repo":  map[string]any{"type": "string", "description": "Repo name. Default: from origin remote."},
	}
	return []*registry.ToolDefinition{
		{
			Name:               "github.pr_list",
			SummaryDescription: "List pull requests, one compact line each. Read-only.",
			LongDescription:    "Lists PRs via the GitHub API and returns `#num [state] title — @author (branch)`, one per line. Scope with `state` (open|closed|all) and `limit`. owner/repo default to the workspace's origin remote.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": merge(repoProps, map[string]any{
					"state": map[string]any{"type": "string", "enum": []any{"open", "closed", "all"}, "description": "Default open."},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Default 20."},
				}),
			}),
			Tags: []string{"github", "read-only", "network"}, Idempotency: "idempotent",
			ErrorModes: "Returns `github: ...` on auth/API failure or when owner/repo can't be resolved.",
			Examples:   []*toolboxv0.ToolExample{{Description: "List open PRs.", Arguments: mustStruct(map[string]any{"limit": 10}), ExpectedOutcome: "Up to 10 compact PR lines."}},
			Handler:    s.prList,
		},
		{
			Name:               "github.pr_view",
			SummaryDescription: "View a PR's body with badges/images/comments stripped. Read-only.",
			LongDescription:    "Fetches a PR and returns title, state, author, and the body run through the gortk gh filter (badge rows, image lines, and HTML comments removed).",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": merge(repoProps, map[string]any{"number": map[string]any{"type": "integer", "minimum": 1, "description": "PR number."}}),
				"required":   []any{"number"},
			}),
			Tags: []string{"github", "read-only", "network"}, Idempotency: "idempotent",
			ErrorModes: "Returns `github: ...` when the PR doesn't exist or auth fails.",
			Examples:   []*toolboxv0.ToolExample{{Description: "View PR #42.", Arguments: mustStruct(map[string]any{"number": 42}), ExpectedOutcome: "Title/state/author plus the de-noised body."}},
			Handler:    s.prView,
		},
		{
			Name:               "github.issue_list",
			SummaryDescription: "List issues (excluding PRs), one compact line each. Read-only.",
			LongDescription:    "Lists issues via the GitHub API (PRs filtered out) as `#num [state] title — @author`. Scope with `state` and `limit`.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": merge(repoProps, map[string]any{
					"state": map[string]any{"type": "string", "enum": []any{"open", "closed", "all"}, "description": "Default open."},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Default 20."},
				}),
			}),
			Tags: []string{"github", "read-only", "network"}, Idempotency: "idempotent",
			ErrorModes: "Returns `github: ...` on failure.",
			Examples:   []*toolboxv0.ToolExample{{Description: "List open issues.", Arguments: mustStruct(map[string]any{}), ExpectedOutcome: "Compact issue lines."}},
			Handler:    s.issueList,
		},
		{
			Name:               "github.run_list",
			SummaryDescription: "List recent CI workflow runs, one compact line each. Read-only.",
			LongDescription:    "Lists recent GitHub Actions runs as `id status/conclusion name (branch)`. Scope with `limit`.",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": merge(repoProps, map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Default 20."}}),
			}),
			Tags: []string{"github", "read-only", "network", "ci"}, Idempotency: "idempotent",
			ErrorModes: "Returns `github: ...` on failure.",
			Examples:   []*toolboxv0.ToolExample{{Description: "Show 10 latest runs.", Arguments: mustStruct(map[string]any{"limit": 10}), ExpectedOutcome: "Compact run lines with conclusions."}},
			Handler:    s.runList,
		},
	}
}

// --- handlers ----------------------------------------------------------------

func (s *Server) prList(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	owner, repo, err := s.resolveRepo(args)
	if err != nil {
		return respond.Error("github: %v", err)
	}
	prs, _, err := s.client.PullRequests.List(ctx, owner, repo, &gh.PullRequestListOptions{
		State:       stateArg(args, "open"),
		ListOptions: gh.ListOptions{PerPage: intArg(args, "limit", 20)},
	})
	if err != nil {
		return respond.Error("github: %v", err)
	}
	items := make([]map[string]any, 0, len(prs))
	for _, pr := range prs {
		items = append(items, map[string]any{
			"number": pr.GetNumber(), "state": pr.GetState(), "title": pr.GetTitle(),
			"author": pr.GetUser().GetLogin(), "branch": pr.GetHead().GetRef(),
		})
	}
	return compact(prFilter, items)
}

func (s *Server) prView(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	owner, repo, err := s.resolveRepo(args)
	if err != nil {
		return respond.Error("github: %v", err)
	}
	n := intArg(args, "number", 0)
	if n <= 0 {
		return respond.Error("github.pr_view: `number` is required")
	}
	pr, _, err := s.client.PullRequests.Get(ctx, owner, repo, n)
	if err != nil {
		return respond.Error("github: %v", err)
	}
	// Strip badge/image/comment noise from the body via the gortk gh filter.
	body := llmout.Compress("gh", []string{"pr", "view"}, pr.GetBody())
	header := fmt.Sprintf("#%d [%s] %s — @%s\n%s\n\n", pr.GetNumber(), pr.GetState(), pr.GetTitle(), pr.GetUser().GetLogin(), pr.GetHTMLURL())
	return respond.Text(header + body)
}

func (s *Server) issueList(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	owner, repo, err := s.resolveRepo(args)
	if err != nil {
		return respond.Error("github: %v", err)
	}
	issues, _, err := s.client.Issues.ListByRepo(ctx, owner, repo, &gh.IssueListByRepoOptions{
		State:       stateArg(args, "open"),
		ListOptions: gh.ListOptions{PerPage: intArg(args, "limit", 20)},
	})
	if err != nil {
		return respond.Error("github: %v", err)
	}
	items := make([]map[string]any, 0, len(issues))
	for _, is := range issues {
		if is.IsPullRequest() { // ListByRepo includes PRs; drop them
			continue
		}
		items = append(items, map[string]any{
			"number": is.GetNumber(), "state": is.GetState(),
			"title": is.GetTitle(), "author": is.GetUser().GetLogin(),
		})
	}
	return compact(issueFilter, items)
}

func (s *Server) runList(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	owner, repo, err := s.resolveRepo(args)
	if err != nil {
		return respond.Error("github: %v", err)
	}
	runs, _, err := s.client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, &gh.ListWorkflowRunsOptions{
		ListOptions: gh.ListOptions{PerPage: intArg(args, "limit", 20)},
	})
	if err != nil {
		return respond.Error("github: %v", err)
	}
	items := make([]map[string]any, 0)
	for _, r := range runs.WorkflowRuns {
		items = append(items, map[string]any{
			"id": r.GetID(), "status": r.GetStatus(), "conclusion": r.GetConclusion(),
			"name": r.GetName(), "branch": r.GetHeadBranch(),
		})
	}
	return compact(runFilter, items)
}

// compact marshals normalized items to JSON and runs the gortk filter over them.
func compact(filter gortk.Filter, items []map[string]any) *toolboxv0.CallToolResponse {
	data, err := json.Marshal(items)
	if err != nil {
		return respond.Error("github: encode: %v", err)
	}
	return respond.Text(filter.Apply(gortk.Command{Stdout: data}).Text)
}

// --- repo resolution ---------------------------------------------------------

// resolveRepo returns owner/repo from args, falling back to the workspace's
// origin remote.
func (s *Server) resolveRepo(args map[string]any) (string, string, error) {
	owner, _ := args["owner"].(string)
	repo, _ := args["repo"].(string)
	if owner != "" && repo != "" {
		return owner, repo, nil
	}
	o, r, err := s.repoFromRemote()
	if err != nil {
		return "", "", fmt.Errorf("resolve repo: %w (pass owner/repo explicitly)", err)
	}
	if owner == "" {
		owner = o
	}
	if repo == "" {
		repo = r
	}
	return owner, repo, nil
}

func (s *Server) repoFromRemote() (string, string, error) {
	repo, err := gogit.PlainOpen(s.workspace)
	if err != nil {
		return "", "", fmt.Errorf("open repo: %w", err)
	}
	rem, err := repo.Remote("origin")
	if err != nil {
		return "", "", fmt.Errorf("origin remote: %w", err)
	}
	urls := rem.Config().URLs
	if len(urls) == 0 {
		return "", "", fmt.Errorf("origin has no URL")
	}
	return parseGitHubRemote(urls[0])
}

// parseGitHubRemote extracts owner/repo from an ssh or https GitHub remote URL.
func parseGitHubRemote(u string) (string, string, error) {
	s := strings.TrimSuffix(u, ".git")
	if i := strings.Index(s, "github.com"); i >= 0 {
		s = s[i+len("github.com"):]
	}
	s = strings.TrimLeft(s, ":/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("not a github remote: %q", u)
	}
	return parts[0], parts[1], nil
}

// --- helpers -----------------------------------------------------------------

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "token "+t.token)
	}
	return t.base.RoundTrip(r)
}

func merge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func stateArg(args map[string]any, def string) string {
	if v, ok := args["state"].(string); ok && v != "" {
		return v
	}
	return def
}

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("github toolbox: cannot encode example args: %v", err))
	}
	return s
}
