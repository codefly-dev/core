package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/web"
)

// hostOf strips the leading "http://" from a httptest server URL
// so we can pass the bare host to WithAllowedDomains.
func hostOf(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	u := strings.TrimPrefix(ts.URL, "http://")
	if i := strings.IndexByte(u, ':'); i >= 0 {
		return u[:i]
	}
	return u
}

func TestWeb_Identity_NoAllowlist(t *testing.T) {
	srv := web.New("0.0.1")
	resp, err := srv.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "web", resp.Name)
	require.Equal(t, []string{"curl", "wget"}, resp.CanonicalFor,
		"web toolbox owns curl + wget; canonical-routing layer reads this")
	require.Contains(t, resp.SandboxSummary, "0 domain")
}

func TestWeb_ListTools_Stable(t *testing.T) {
	srv := web.New("0.0.1")
	resp, err := srv.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{"web.fetch"}, names,
		"if the tool surface changes, pin it here")
}

func TestWeb_Fetch_DenyByDefault(t *testing.T) {
	// New server with no allowed domains; every fetch refused.
	srv := web.New("0.0.1")

	args, _ := structpb.NewStruct(map[string]any{"url": "https://example.com"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "allowlist",
		"refusal must mention allowlist so the agent knows the failure mode")
	require.Contains(t, resp.Error, "example.com")
}

func TestWeb_Fetch_AllowsListedHost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer ts.Close()

	srv := web.New("0.0.1").WithAllowedDomains(hostOf(t, ts))
	args, _ := structpb.NewStruct(map[string]any{"url": ts.URL + "/path"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "allowlisted host should fetch successfully")

	out := resp.Content[0].GetStructured().AsMap()
	require.EqualValues(t, 200, out["status_code"], "status_code is the integer code (post-split)")
	require.Equal(t, "OK", out["status_text"], "status_text is the reason phrase only — no leading code")
	require.Equal(t, "hello", out["body"])
	require.Equal(t, false, out["truncated"])
}

func TestWeb_Fetch_AllowsAnyPortOnAllowedHost(t *testing.T) {
	// httptest binds to an ephemeral port. Allowlist entry is the
	// bare hostname; the explicit port in the URL must not gate the
	// match. Pins the documented behavior: hostname is the unit of
	// trust, port is not.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer ts.Close()

	srv := web.New("0.0.1").WithAllowedDomains(hostOf(t, ts))
	args, _ := structpb.NewStruct(map[string]any{"url": ts.URL})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"explicit port in URL must not gate the allowlist match — hostname is the unit of trust")
}

func TestWeb_Fetch_RejectsNonHTTP(t *testing.T) {
	srv := web.New("0.0.1").WithAllowedDomains("example.com")

	args, _ := structpb.NewStruct(map[string]any{"url": "file:///etc/passwd"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "http/https",
		"non-http schemes are a category error and must be refused without surprise")
}

func TestWeb_Fetch_HonorsAllowlistCaseInsensitive(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	// Allowlist with uppercase, URL with lowercase — should still match.
	srv := web.New("0.0.1").WithAllowedDomains(strings.ToUpper(hostOf(t, ts)))
	args, _ := structpb.NewStruct(map[string]any{"url": ts.URL})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"hostnames are case-insensitive; allowlist match must mirror that")
}

func TestWeb_Fetch_TruncatesLargeBody(t *testing.T) {
	// Server returns a body larger than MaxBodyBytes so we can
	// observe the truncated flag and the cap on len(body).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Just over 4 MiB.
		_, _ = w.Write([]byte(strings.Repeat("x", web.MaxBodyBytes+100)))
	}))
	defer ts.Close()

	srv := web.New("0.0.1").WithAllowedDomains(hostOf(t, ts))
	args, _ := structpb.NewStruct(map[string]any{"url": ts.URL})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, true, out["truncated"], "oversize body must surface the truncation flag")
	body, _ := out["body"].(string)
	require.Len(t, body, web.MaxBodyBytes)
}

func TestWeb_Fetch_BlocksRedirectToOffAllowlistHost(t *testing.T) {
	// A trampoline server on the allowlist redirects to a host that
	// is explicitly NOT on the allowlist. CheckRedirect must fire
	// and refuse before the request to the off-host issues.
	//
	// Why a fake DNS name (instead of a second httptest server):
	// httptest binds to 127.0.0.1 on every port, and our allowlist
	// matches by hostname only (port-agnostic — documented behavior).
	// Two httptest servers would BOTH be on `127.0.0.1` and so both
	// allowed by accident. Using `evil.invalid.test.localdomain` is
	// guaranteed to NOT be on the allowlist, and CheckRedirect must
	// block before any DNS lookup ever happens.
	const offHostURL = "http://evil.invalid.test.localdomain/path"

	trampoline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", offHostURL)
		w.WriteHeader(http.StatusFound)
	}))
	defer trampoline.Close()

	srv := web.New("0.0.1").WithAllowedDomains(hostOf(t, trampoline))

	args, _ := structpb.NewStruct(map[string]any{"url": trampoline.URL})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "web.fetch",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"redirect to off-allowlist host MUST be blocked; not following silently")
	require.Contains(t, resp.Error, "evil.invalid.test.localdomain",
		"error should name the blocked target so the agent knows what was rejected")
}

func TestWeb_Fetch_UnknownTool_ActionableError(t *testing.T) {
	srv := web.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "web.bogus"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "web.bogus")
	require.Contains(t, resp.Error, "ListToolSummaries")
}
