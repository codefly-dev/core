package manager

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// routeReleaseAPI points the loader's release client at server for the test:
// every request the client makes, to api.github.com included, reaches it.
func routeReleaseAPI(t *testing.T, server *httptest.Server) {
	t.Helper()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	previous := agentDownloadClient
	agentDownloadClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	t.Cleanup(func() { agentDownloadClient = previous })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A release the anonymous API cannot see answers 404 whether it is missing or
// private; the installed source's credential is what reads a private one.
func privateReleaseAPI(t *testing.T, token string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.0","assets":[]}`))
	}))
	t.Cleanup(server.Close)
	routeReleaseAPI(t, server)
}

func TestTheInstalledReleaseTokenSourceReadsAPrivateRelease(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	privateReleaseAPI(t, "host-credential")
	t.Cleanup(func() { UseReleaseTokenSource(nil) })

	// The environment alone cannot read it, and says what it tried.
	_, err := openAgentRelease(t.Context(), testReleaseURL)
	require.ErrorContains(t, err, "404")
	require.ErrorContains(t, err, "tried GITHUB_TOKEN, GH_TOKEN")

	// The host's source can: the release resolves, and the refusal that
	// follows is about the asset, not the credential.
	UseReleaseTokenSource(func() (string, []string) { return "host-credential", nil })
	_, err = openAgentRelease(t.Context(), testReleaseURL)
	require.ErrorContains(t, err, "does not contain the requested asset")

	// A credential that cannot read it is refused without being repeated.
	UseReleaseTokenSource(func() (string, []string) { return "wrong-credential", nil })
	_, err = openAgentRelease(t.Context(), testReleaseURL)
	require.ErrorContains(t, err, "404")
	require.NotContains(t, err.Error(), "wrong-credential")
}

func TestARefusedPrivateReleaseNamesEverySourceTheHostTried(t *testing.T) {
	privateReleaseAPI(t, "unreachable")
	t.Cleanup(func() { UseReleaseTokenSource(nil) })
	UseReleaseTokenSource(func() (string, []string) {
		return "", []string{"GITHUB_TOKEN", "GH_TOKEN", "the gh login for github.com (not logged in)"}
	})
	_, err := openAgentRelease(t.Context(), testReleaseURL)
	require.ErrorContains(t, err, "tried GITHUB_TOKEN, GH_TOKEN, the gh login for github.com (not logged in)")
}

func TestTheEnvironmentStillTakesPrecedenceByDefault(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "from-environment")
	UseReleaseTokenSource(nil)
	token, _ := ReleaseToken()
	require.Equal(t, "from-environment", token)
}
