package urlguard_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codefly-dev/core/network/urlguard"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOrigin_Canonicalization(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want urlguard.Origin
	}{
		{"lowercases host", "https://API.Example.COM", urlguard.Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
		{"strips trailing dot", "https://api.example.com.", urlguard.Origin{Scheme: "https", Host: "api.example.com", Port: 443}},
		{"explicit port", "https://api.example.com:8443", urlguard.Origin{Scheme: "https", Host: "api.example.com", Port: 8443}},
		{"http default port", "http://api.example.com", urlguard.Origin{Scheme: "http", Host: "api.example.com", Port: 80}},
		{"ipv6 literal", "https://[2606:4700:4700::1111]", urlguard.Origin{Scheme: "https", Host: "2606:4700:4700::1111", Port: 443}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := urlguard.NormalizeOrigin(tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizeOrigin_Rejections(t *testing.T) {
	cases := map[string]string{
		"userinfo":         "https://user:pass@api.example.com",
		"userinfo no pass": "https://user@api.example.com",
		"path":             "https://api.example.com/v1",
		"query":            "https://api.example.com?x=1",
		"fragment":         "https://api.example.com#frag",
		"scheme relative":  "//api.example.com",
		"non http scheme":  "file://api.example.com",
		"ftp scheme":       "ftp://api.example.com",
		"empty":            "",
		"opaque":           "mailto:x@example.com",
		"unicode host":     "https://exämple.com", // must be submitted as punycode, not raw unicode
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := urlguard.NormalizeOrigin(raw)
			require.Error(t, err, "expected %q to be rejected", raw)
		})
	}
}

func TestSafePath(t *testing.T) {
	ok := []string{"/", "/v1/resources", "/v1/resources/abc-123"}
	for _, p := range ok {
		got, err := urlguard.SafePath(p)
		require.NoError(t, err, p)
		require.NotEmpty(t, got)
	}
	bad := []string{"relative", "/a/../b", "/a/%2e%2e/b", "/a%2fb", "/a?x=1", "/a#f", "/a//b", "/a\\b"}
	for _, p := range bad {
		_, err := urlguard.SafePath(p)
		require.Error(t, err, p)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]urlguard.NetworkClass{
		"8.8.8.8":         urlguard.ClassPublic,
		"127.0.0.1":       urlguard.ClassLoopback,
		"::1":             urlguard.ClassLoopback,
		"169.254.169.254": urlguard.ClassLinkLocal, // cloud metadata
		"fe80::1":         urlguard.ClassLinkLocal,
		"10.1.2.3":        urlguard.ClassPrivate,
		"192.168.0.1":     urlguard.ClassPrivate,
		"172.16.0.1":      urlguard.ClassPrivate,
		"100.64.0.1":      urlguard.ClassPrivate,   // CGNAT
		"fc00::1":         urlguard.ClassPrivate,   // ULA
		"0.0.0.0":         urlguard.ClassPrivate,   // unspecified
		"239.0.0.1":       urlguard.ClassPrivate,   // administratively-scoped multicast
		"224.0.0.1":       urlguard.ClassLinkLocal, // link-local multicast
	}
	for raw, want := range cases {
		require.Equal(t, want, urlguard.Classify(net.ParseIP(raw)), raw)
	}
}

func TestResolve_LiteralPrivateRejectedUnlessAllowed(t *testing.T) {
	origin, err := urlguard.NormalizeOrigin("https://10.0.0.5")
	require.NoError(t, err)

	_, err = urlguard.Resolve(context.Background(), nil, origin, nil)
	require.Error(t, err)

	res, err := urlguard.Resolve(context.Background(), nil, origin, []urlguard.NetworkClass{urlguard.ClassPrivate})
	require.NoError(t, err)
	require.Equal(t, urlguard.ClassPrivate, res.Class)
}

func TestResolve_MetadataRejected(t *testing.T) {
	origin, err := urlguard.NormalizeOrigin("http://169.254.169.254")
	require.NoError(t, err)
	// Not admitted even with private allowed: metadata is link-local.
	_, err = urlguard.Resolve(context.Background(), nil, origin, []urlguard.NetworkClass{urlguard.ClassPrivate})
	require.Error(t, err)
}

func TestResolve_LocalhostRequiresLoopbackAdmission(t *testing.T) {
	origin, err := urlguard.NormalizeOrigin("http://localhost")
	require.NoError(t, err)

	_, err = urlguard.Resolve(context.Background(), nil, origin, nil)
	require.Error(t, err)

	res, err := urlguard.Resolve(context.Background(), nil, origin, []urlguard.NetworkClass{urlguard.ClassLoopback})
	require.NoError(t, err)
	require.Equal(t, urlguard.ClassLoopback, res.Class)
}

// TestClient_PinsPeerIP proves the client dials the pinned IP regardless of
// the host in the request URL — the DNS-rebinding defense — and that no proxy
// environment variable can divert it.
func TestClient_PinsPeerIP(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9") // canary: must be ignored
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	port := mustPort(t, portStr)

	origin := urlguard.Origin{Scheme: "http", Host: "pinned.invalid", Port: port}
	pinned := urlguard.Resolution{IP: net.ParseIP(host), Class: urlguard.ClassLoopback}
	client := urlguard.Client(origin, pinned, urlguard.DefaultDeadlines())

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy, "transport must have no proxy")

	// URL host is a name that does not resolve to the server; the pinned dial
	// reaches it anyway.
	resp, err := client.Get(origin.String() + "/anything")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_RefusesRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/other", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	origin := urlguard.Origin{Scheme: "http", Host: "pinned.invalid", Port: mustPort(t, portStr)}
	pinned := urlguard.Resolution{IP: net.ParseIP(host), Class: urlguard.ClassLoopback}
	client := urlguard.Client(origin, pinned, urlguard.DefaultDeadlines())

	resp, err := client.Get(origin.String() + "/start")
	require.NoError(t, err)
	defer resp.Body.Close()
	// The redirect is surfaced, never followed.
	require.Equal(t, http.StatusFound, resp.StatusCode)
}

func TestCeiling_Admit(t *testing.T) {
	ceiling := urlguard.Ceiling{
		Schemes:      []string{"https"},
		HostPatterns: []string{"api.stripe.com", "*.sentry.io"},
		Ports:        []uint32{443},
	}
	ok := []string{"https://api.stripe.com", "https://o1.ingest.sentry.io", "https://API.Stripe.com."}
	for _, raw := range ok {
		_, err := ceiling.Admit(raw)
		require.NoError(t, err, raw)
	}
	bad := []string{
		"http://api.stripe.com",       // scheme
		"https://api.stripe.com:8443", // port
		"https://evil.com",            // host
		"https://sentry.io",           // wildcard apex not matched
		"https://api.stripe.com.evil.com",
		"https://user@api.stripe.com", // userinfo
	}
	for _, raw := range bad {
		_, err := ceiling.Admit(raw)
		require.Error(t, err, raw)
	}
}

func mustPort(t *testing.T, s string) uint32 {
	t.Helper()
	var n uint32
	for _, r := range s {
		require.True(t, r >= '0' && r <= '9')
		n = n*10 + uint32(r-'0')
	}
	return n
}
