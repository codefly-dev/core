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
	gh "github.com/google/go-github/v89/github"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	gatewayv1 "github.com/codefly-dev/core/generated/go/mind/gateway/v1"
	"github.com/codefly-dev/core/llmout"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
	"github.com/codefly-dev/gortk"
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
	client    *gh.Client
}

// New returns a Server using the given GitHub token, scoped to a workspace whose
// origin remote names the default owner/repo.
func New(workspace, token, version string) *Server {
	httpClient := &http.Client{Transport: tokenTransport{token: token, base: http.DefaultTransport}}
	client, err := gh.NewClient(gh.WithHTTPClient(httpClient))
	if err != nil {
		panic(fmt.Sprintf("github toolbox: configure client: %v", err))
	}
	s := &Server{
		workspace: workspace,
		client:    client,
	}
	s.Base = registry.NewBase(registry.Descriptor{
		Name:           "github",
		Version:        version,
		Description:    "GitHub PRs, issues, and CI runs via the go-github SDK; output compacted for LLM context.",
		CanonicalFor:   []string{"github"},
		SandboxSummary: "network required (api.github.com); reads GitHub via GITHUB_TOKEN; owner/repo from origin remote",
	}, s.Tools()...)
	return s
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
		{
			Name:               "forge.pr_status",
			SummaryDescription: "Return typed PR, required-check, and review state. Read-only.",
			LongDescription:    "Returns one provider-neutral pull-request snapshot with mergeability, checks, reviews, and a blocking reason. The GitHub API is authoritative; callers never need to parse command output.",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": merge(repoProps, map[string]any{"number": map[string]any{"type": "integer", "minimum": 1, "description": "Pull-request number."}}),
				"required":   []any{"number"},
			}),
			Tags: []string{"forge", "github", "read-only", "network", "ci"}, Idempotency: "idempotent",
			ErrorModes: "Returns `github: ...` on auth/API failure or when owner/repo cannot be resolved.",
			Examples:   []*toolboxv0.ToolExample{{Description: "Read PR #42 status.", Arguments: mustStruct(map[string]any{"number": 42}), ExpectedOutcome: "Typed mergeability, checks, reviews, and blocking reason."}},
			Handler:    s.forgePRStatus,
		},
		{
			Name:               "forge.merge_pr",
			SummaryDescription: "Merge a PR with an explicit method and check policy.",
			LongDescription:    "Fetches authoritative status, enforces required checks unless bypass is explicitly selected, merges through the GitHub API, and returns a stable act identity that reconciles with the inbound merge event.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": merge(repoProps, map[string]any{
					"number":         map[string]any{"type": "integer", "minimum": 1, "description": "Pull-request number."},
					"method":         map[string]any{"type": "string", "enum": []any{"merge", "squash", "rebase"}, "description": "Merge strategy."},
					"check_policy":   map[string]any{"type": "string", "enum": []any{"require_passing", "bypass"}, "description": "Required-check policy."},
					"commit_title":   map[string]any{"type": "string", "description": "Optional merge commit title."},
					"commit_message": map[string]any{"type": "string", "description": "Optional merge commit body."},
				}),
				"required": []any{"number", "method", "check_policy"},
			}),
			Destructive: true,
			Tags:        []string{"forge", "github", "network", "destructive"}, Idempotency: "side_effecting",
			ErrorModes: "Returns a typed blocked response when required checks/reviews do not pass; API failures return `github: ...`.",
			Examples:   []*toolboxv0.ToolExample{{Description: "Squash PR #42 after required checks pass.", Arguments: mustStruct(map[string]any{"number": 42, "method": "squash", "check_policy": "require_passing"}), ExpectedOutcome: "Provider-confirmed merge receipt and post-merge status."}},
			Handler:    s.forgeMergePR,
		},
		{
			Name:               "forge.request_review",
			SummaryDescription: "Request users or teams to review a PR.",
			LongDescription:    "Requests reviewers through the GitHub API and returns the provider-accepted reviewer set plus a stable act identity.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": merge(repoProps, map[string]any{
					"number":         map[string]any{"type": "integer", "minimum": 1, "description": "Pull-request number."},
					"reviewers":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "User logins."},
					"team_reviewers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Team slugs."},
				}),
				"required": []any{"number"},
			}),
			Destructive: true,
			Tags:        []string{"forge", "github", "network", "destructive"}, Idempotency: "side_effecting",
			ErrorModes: "Returns `github: ...` on auth/API failure; rejects calls with no user or team reviewers.",
			Examples:   []*toolboxv0.ToolExample{{Description: "Request alice on PR #42.", Arguments: mustStruct(map[string]any{"number": 42, "reviewers": []any{"alice"}}), ExpectedOutcome: "Provider-confirmed reviewer request receipt."}},
			Handler:    s.forgeRequestReview,
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

func (s *Server) forgePRStatus(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	status, err := s.PullRequestStatus(ctx, forgeRepositoryArg(args), int64(intArg(args, "number", 0)))
	if err != nil {
		return respond.Error("github: %v", err)
	}
	return protoResponse(status)
}

func (s *Server) forgeMergePR(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	result, err := s.MergePullRequest(ctx, &gatewayv1.ForgeMergePullRequestRequest{
		Repository:    forgeRepositoryArg(args),
		Number:        int64(intArg(args, "number", 0)),
		Method:        forgeMergeMethodArg(args),
		CheckPolicy:   forgeCheckPolicyArg(args),
		CommitTitle:   stringArg(args, "commit_title"),
		CommitMessage: stringArg(args, "commit_message"),
	})
	if err != nil {
		return respond.Error("github: %v", err)
	}
	return protoResponse(result)
}

func (s *Server) forgeRequestReview(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	result, err := s.RequestReview(ctx, &gatewayv1.ForgeRequestReviewRequest{
		Repository:    forgeRepositoryArg(args),
		Number:        int64(intArg(args, "number", 0)),
		Reviewers:     stringSliceArg(args, "reviewers"),
		TeamReviewers: stringSliceArg(args, "team_reviewers"),
	})
	if err != nil {
		return respond.Error("github: %v", err)
	}
	return protoResponse(result)
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

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func stringSliceArg(args map[string]any, key string) []string {
	values, _ := args[key].([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func forgeRepositoryArg(args map[string]any) *gatewayv1.ForgeRepository {
	return &gatewayv1.ForgeRepository{
		Provider: "github",
		Owner:    stringArg(args, "owner"),
		Name:     stringArg(args, "repo"),
	}
}

func forgeMergeMethodArg(args map[string]any) gatewayv1.ForgeMergeMethod {
	switch stringArg(args, "method") {
	case "merge":
		return gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_MERGE
	case "squash":
		return gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_SQUASH
	case "rebase":
		return gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_REBASE
	default:
		return gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_UNSPECIFIED
	}
}

func forgeCheckPolicyArg(args map[string]any) gatewayv1.ForgeCheckPolicy {
	switch stringArg(args, "check_policy") {
	case "require_passing":
		return gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_REQUIRE_PASSING
	case "bypass":
		return gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_BYPASS
	default:
		return gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_UNSPECIFIED
	}
}

func protoResponse(message proto.Message) *toolboxv0.CallToolResponse {
	body, err := protojson.Marshal(message)
	if err != nil {
		return respond.Error("github: encode response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return respond.Error("github: decode response: %v", err)
	}
	return respond.Struct(payload)
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("github toolbox: cannot encode example args: %v", err))
	}
	return s
}
