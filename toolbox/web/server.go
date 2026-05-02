package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/internal/registry"
	"github.com/codefly-dev/core/toolbox/internal/respond"
)

// MaxBodyBytes caps any single fetch's response body. Above this,
// the toolbox truncates and surfaces a `truncated: true` flag rather
// than buffering unbounded bytes — defends the codefly host against
// a hostile target serving a multi-GB stream.
const MaxBodyBytes = 4 * 1024 * 1024 // 4 MiB

// DefaultTimeout caps any single fetch. Configurable per-call via
// the timeout_ms argument; this is the floor when no value is
// supplied.
const DefaultTimeout = 30 * time.Second

// Server implements codefly.services.toolbox.v0.Toolbox for HTTP
// fetch + (placeholder) search.
//
// Network policy is enforced at two layers:
//
//  1. Inside this toolbox: a domain allowlist (set via
//     WithAllowedDomains) — the toolbox refuses any URL whose host
//     isn't on the list before issuing the request.
//  2. Outside (host): the OS sandbox can additionally restrict
//     network to specific outbound paths. The toolbox doesn't
//     assume the OS layer is in place; the layer-1 check is
//     authoritative on its own.
type Server struct {
	toolboxv0.UnimplementedToolboxServer

	version        string
	allowedDomains map[string]struct{}
	httpClient     *http.Client
}

// New returns a Server with no allowed domains (every fetch is
// refused until WithAllowedDomains is called). This is the
// safe-by-default position — adding domains is an explicit policy
// decision the operator must make.
//
// The http.Client is configured with a CheckRedirect that
// re-validates every redirect target against the allowlist.
// Without this guard, a target ON the allowlist could 302 to a
// target OFF the allowlist and the toolbox would silently follow,
// producing the unauthorized request the policy was meant to
// prevent. Found in code-review pass; not exploitable today
// because no redirects-leaving-allowlist were exercised, but the
// guard is the only correct behavior.
func New(version string) *Server {
	s := &Server{
		version:        version,
		allowedDomains: make(map[string]struct{}),
	}
	// CheckRedirect uses a closure (not the bound method value) so
	// the function captured here always reads s.allowedDomains
	// freshly — including any domains added by WithAllowedDomains
	// AFTER construction. A bound method value on Server would
	// also work, but the closure form is the idiom Go's net/http
	// docs suggest, and it's clearer at the call site that we're
	// intentionally re-reading state per redirect.
	s.httpClient = &http.Client{
		Timeout: DefaultTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := strings.ToLower(req.URL.Hostname())
			if _, ok := s.allowedDomains[host]; !ok {
				return fmt.Errorf("redirect to %q blocked: host not on allowlist", host)
			}
			return nil
		},
	}
	return s
}


// WithAllowedDomains adds hosts to the allowlist. Match semantics:
//
//   - Hostname-only: subdomain match is EXACT. "example.com" allows
//     https://example.com/x but NOT https://api.example.com/x —
//     that's a separate entry. Strict matching avoids the wildcard-
//     subdomain footgun where a typo'd `*.example.com` allows
//     attacker-controlled `evilexample.com`.
//
//   - Ports: the URL hostname is matched WITHOUT the port. An entry
//     of "example.com" allows both `example.com` and `example.com:8080`.
//     If you need port-based isolation, wrap requests in a service
//     boundary; the toolbox treats hostnames as the unit of trust.
func (s *Server) WithAllowedDomains(domains ...string) *Server {
	for _, d := range domains {
		s.allowedDomains[strings.ToLower(d)] = struct{}{}
	}
	return s
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	allowed := make([]string, 0, len(s.allowedDomains))
	for d := range s.allowedDomains {
		allowed = append(allowed, d)
	}
	return &toolboxv0.IdentityResponse{
		Name:        "web",
		Version:     s.version,
		Description: "HTTP fetch behind a domain allowlist; canonical replacement for curl/wget.",
		CanonicalFor: []string{"curl", "wget"},
		SandboxSummary: fmt.Sprintf(
			"network: allowlist (%d domain(s)); reads: deny; writes: deny",
			len(allowed)),
	}, nil
}

// --- Tools -------------------------------------------------------

// tools is the single source of truth for this toolbox's surface.
// See git/server.go for the convention authors should follow.
func (s *Server) tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "web.fetch",
			SummaryDescription: fmt.Sprintf("Single HTTP request behind a domain allowlist; returns status/headers/body. Body cap %d bytes.", MaxBodyBytes),
			LongDescription: "Issue a single HTTP request to a URL whose host appears on the toolbox's " +
				"domain allowlist. Returns the status code, response headers (multi-value joined by ', '), " +
				"and body up to the per-call cap. Bodies above the cap are truncated and the response " +
				"surfaces a `truncated: true` flag.\n\n" +
				"Auth tokens MUST come from host config (NEVER from the agent's input arguments) — the " +
				"toolbox doesn't carry secret material in the contract; secrets enter via env vars at " +
				"plugin spawn time. Redirects to off-allowlist hosts are blocked even if the original " +
				"target is allowed.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Absolute URL. Host must be on the toolbox's allowlist.",
					},
					"method": map[string]any{
						"type":        "string",
						"description": "HTTP method. Default GET.",
						"enum":        []any{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH"},
					},
					"timeout_ms": map[string]any{
						"type":        "integer",
						"description": "Per-call timeout. Default 30000.",
						"minimum":     100,
						"maximum":     120000,
					},
					"headers": map[string]any{
						"type":        "object",
						"description": "Header name → value map. Auth tokens MUST come from host config, not the agent.",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Request body (for POST/PUT/PATCH).",
					},
				},
				"required": []any{"url"},
			}),
			Tags:        []string{"web", "network", "read-only"},
			Idempotency: "side_effecting",
			ErrorModes: "Returns error envelope with one of: 'host X not on allowlist' (denied by " +
				"toolbox), 'redirect to X blocked' (target redirected off-allowlist), 'invalid URL', or " +
				"the underlying transport error. Status code 4xx/5xx still returns success at the tool " +
				"level — those are server responses, not tool failures.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Simple GET against an allowlisted host.",
					Arguments:       mustWebStruct(map[string]any{"url": "https://api.example.com/v1/status"}),
					ExpectedOutcome: "{ status_code: 200, status_text: 'OK', headers: {...}, body: '...', truncated: false }",
				},
				{
					Description:     "POST with JSON body.",
					Arguments:       mustWebStruct(map[string]any{"url": "https://api.example.com/v1/items", "method": "POST", "headers": map[string]any{"Content-Type": "application/json"}, "body": `{"name": "x"}`}),
					ExpectedOutcome: "Returns the response from the API. Body cap applies; truncation surfaces in the flag.",
				},
			},
		},
	}
}

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{Tools: registry.AsTools(s.tools())}, nil
}

func (s *Server) ListToolSummaries(_ context.Context, req *toolboxv0.ListToolSummariesRequest) (*toolboxv0.ListToolSummariesResponse, error) {
	return &toolboxv0.ListToolSummariesResponse{Tools: registry.AsSummaries(s.tools(), req.GetTagsFilter())}, nil
}

func (s *Server) DescribeTool(_ context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	spec := registry.FindSpec(s.tools(), req.GetName())
	if spec == nil {
		return &toolboxv0.DescribeToolResponse{
			Error: fmt.Sprintf("unknown tool %q (call ListToolSummaries to enumerate)", req.GetName()),
		}, nil
	}
	return &toolboxv0.DescribeToolResponse{Tool: spec}, nil
}

// mustWebStruct mirrors git's mustStruct — a tiny helper for the
// inline tools() example values. Panics only on programmer-typo
// inputs (literal map[string]any always succeeds).
func mustWebStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("web toolbox: cannot encode example args: %v", err))
	}
	return s
}

func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "web.fetch":
		return s.fetch(ctx, req)
	default:
		return respond.Error("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementation -----------------------------------------

func (s *Server) fetch(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return respond.Error("web.fetch: url is required"), nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return respond.Error("web.fetch: invalid URL: %v", err), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return respond.Error("web.fetch: only http/https URLs are allowed (got %q)", u.Scheme), nil
	}
	host := strings.ToLower(u.Hostname())
	if _, allowed := s.allowedDomains[host]; !allowed {
		return respond.Error("web.fetch: host %q not on allowlist; ask the operator to add it", host), nil
	}

	method := "GET"
	if v, ok := args["method"].(string); ok && v != "" {
		method = strings.ToUpper(v)
	}

	timeout := DefaultTimeout
	if v, ok := args["timeout_ms"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}

	var body io.Reader
	if b, ok := args["body"].(string); ok && b != "" {
		body = strings.NewReader(b)
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, method, rawURL, body)
	if err != nil {
		return respond.Error("web.fetch: build request: %v", err), nil
	}
	if hdrs, ok := args["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			if vs, ok := v.(string); ok {
				httpReq.Header.Set(k, vs)
			}
		}
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return respond.Error("web.fetch: %v", err), nil
	}
	defer resp.Body.Close()

	// Cap the body to defend against unbounded streams. Reading a
	// SectionReader-style cap requires LimitReader + an explicit
	// "did we hit the cap" probe.
	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return respond.Error("web.fetch: read body: %v", err), nil
	}
	truncated := false
	if int64(len(bodyBytes)) > MaxBodyBytes {
		bodyBytes = bodyBytes[:MaxBodyBytes]
		truncated = true
	}

	headers := map[string]any{}
	for k, vals := range resp.Header {
		// Multi-value headers are joined with ", " — same convention
		// as Header.Get's cousin Header.Values, but flattened to one
		// string per key for the JSON envelope.
		headers[k] = strings.Join(vals, ", ")
	}

	// Split the status into separate code + reason fields. resp.Status
	// is like "200 OK" — agents parsing it as an integer would fail,
	// and there's no clean way to signal the split implicitly. Two
	// fields removes the ambiguity at zero cost.
	statusText := resp.Status
	if idx := strings.IndexByte(statusText, ' '); idx > 0 {
		statusText = statusText[idx+1:]
	}
	payload := map[string]any{
		"status_code": resp.StatusCode, // canonical: integer status (200)
		"status_text": statusText,      // reason phrase only ("OK")
		"headers":     headers,
		"body":        string(bodyBytes),
		"truncated":   truncated,
	}
	return respond.Struct(payload), nil
}

