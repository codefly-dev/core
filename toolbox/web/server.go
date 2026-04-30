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
func New(version string) *Server {
	return &Server{
		version:        version,
		allowedDomains: make(map[string]struct{}),
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// WithAllowedDomains adds hosts to the allowlist. Subdomain matching
// is exact: "example.com" allows https://example.com/x but NOT
// https://api.example.com/x — that's a separate entry. Strict
// matching avoids the wildcard-subdomain footgun where a typo'd
// `*.example.com` allows attacker-controlled `evilexample.com.`.
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

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{
		Tools: []*toolboxv0.Tool{
			{
				Name:        "web.fetch",
				Description: "Issue a single HTTP request; returns status, headers, body. Body capped at " + fmt.Sprintf("%d", MaxBodyBytes) + " bytes.",
				InputSchema: mustSchema(map[string]any{
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
				Destructive: false, // GET is non-destructive; mutating methods are still gated by allowlist
			},
		},
	}, nil
}

func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "web.fetch":
		return s.fetch(ctx, req)
	default:
		return errResp("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementation -----------------------------------------

func (s *Server) fetch(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := argMap(req)
	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return errResp("web.fetch: url is required"), nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return errResp("web.fetch: invalid URL: %v", err), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errResp("web.fetch: only http/https URLs are allowed (got %q)", u.Scheme), nil
	}
	host := strings.ToLower(u.Hostname())
	if _, allowed := s.allowedDomains[host]; !allowed {
		return errResp("web.fetch: host %q not on allowlist; ask the operator to add it", host), nil
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
		return errResp("web.fetch: build request: %v", err), nil
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
		return errResp("web.fetch: %v", err), nil
	}
	defer resp.Body.Close()

	// Cap the body to defend against unbounded streams. Reading a
	// SectionReader-style cap requires LimitReader + an explicit
	// "did we hit the cap" probe.
	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return errResp("web.fetch: read body: %v", err), nil
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

	payload := map[string]any{
		"status":     resp.StatusCode,
		"status_text": resp.Status,
		"headers":    headers,
		"body":       string(bodyBytes),
		"truncated":  truncated,
	}
	return structResp(payload), nil
}

// --- Helpers (mirror toolbox/git for consistency) ----------------

func argMap(req *toolboxv0.CallToolRequest) map[string]any {
	if req.Arguments == nil {
		return map[string]any{}
	}
	return req.Arguments.AsMap()
}

func errResp(format string, args ...any) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

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

func mustSchema(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("bad input schema: %v", err))
	}
	return s
}
