package manager

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// routeReleaseAPI points the loader's release client at server for the test:
// every request the client makes, to api.github.com included, reaches it,
// and each response still names the URL the client asked for.
func routeReleaseAPI(t *testing.T, server *httptest.Server) {
	t.Helper()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	previous := agentDownloadClient
	agentDownloadClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		routed := r.Clone(r.Context())
		routed.URL.Scheme, routed.URL.Host = target.Scheme, target.Host
		response, err := http.DefaultTransport.RoundTrip(routed)
		if response != nil {
			// As a real transport would: the response answers the request
			// the client sent, not its rerouted copy.
			response.Request = r
		}
		return response, err
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

// refusingReleaseAPI answers every request with status, whatever credential
// it carries.
func refusingReleaseAPI(t *testing.T, status int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"refused"}`))
	}))
	t.Cleanup(server.Close)
	routeReleaseAPI(t, server)
}

// A token that reaches GitHub and is still refused is named by where it came
// from, so the person knows which credential to fix — and never by its value.
func TestARefusedTokenNamesTheSourceItCameFrom(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		github   string
		gh       string
		source   ReleaseTokenSource
		secret   string
		contains string
	}{
		{name: "GITHUB_TOKEN refused", status: http.StatusNotFound, github: "env-secret-1", secret: "env-secret-1",
			contains: "beyond what the credential from GITHUB_TOKEN can read"},
		{name: "GH_TOKEN refused", status: http.StatusNotFound, gh: "env-secret-2", secret: "env-secret-2",
			contains: "beyond what the credential from GH_TOKEN can read"},
		{name: "GITHUB_TOKEN not accepted", status: http.StatusUnauthorized, github: "env-secret-3", secret: "env-secret-3",
			contains: "GitHub did not accept the credential from GITHUB_TOKEN"},
		{name: "GITHUB_TOKEN forbidden", status: http.StatusForbidden, github: "env-secret-4", secret: "env-secret-4",
			contains: "the credential from GITHUB_TOKEN is not allowed to read this release"},
		{name: "installed source names its origin", status: http.StatusUnauthorized, secret: "host-secret-1",
			source: func() (string, []string) {
				return "host-secret-1", []string{"the gh CLI login for github.com"}
			},
			contains: "GitHub did not accept the credential from the gh CLI login for github.com"},
		{name: "installed source names nothing", status: http.StatusNotFound, secret: "host-secret-2",
			source:   func() (string, []string) { return "host-secret-2", nil },
			contains: "beyond what the credential from the host's release-token source can read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", tc.github)
			t.Setenv("GH_TOKEN", tc.gh)
			refusingReleaseAPI(t, tc.status)
			t.Cleanup(func() { UseReleaseTokenSource(nil) })
			UseReleaseTokenSource(tc.source)

			_, err := openAgentRelease(t.Context(), testReleaseURL)
			require.ErrorContains(t, err, tc.contains)
			require.NotContains(t, err.Error(), tc.secret)

			_, err = githubLatestReleaseTag(t.Context(), "example-org", "service-render")
			require.ErrorContains(t, err, tc.contains)
			require.NotContains(t, err.Error(), tc.secret)
		})
	}
}

// The release lookups send the credential to the GitHub API and nowhere else:
// a redirect within the API keeps it, one to another host drops it — as the
// asset download, which reads redirected bytes anonymously, already does.
func TestReleaseLookupsDropTheTokenOnACrossHostRedirect(t *testing.T) {
	lookups := map[string]func(t *testing.T) error{
		"release by tag": func(t *testing.T) error {
			_, err := openAgentRelease(t.Context(), testReleaseURL)
			return err
		},
		"latest release": func(t *testing.T) error {
			_, err := githubLatestReleaseTag(t.Context(), "example-org", "service-render")
			return err
		},
	}
	targets := map[string]struct {
		location string
		want     string
	}{
		"same host":             {location: "https://api.github.com/moved", want: "Bearer redirect-secret"},
		"other host":            {location: "https://elsewhere.example/moved", want: ""},
		"same host over http":   {location: "http://api.github.com/moved", want: ""},
		"same host, other port": {location: "https://api.github.com:8443/moved", want: ""},
	}
	for lookup, call := range lookups {
		for target, tc := range targets {
			t.Run(lookup+" to "+target, func(t *testing.T) {
				t.Setenv("GITHUB_TOKEN", "redirect-secret")
				t.Setenv("GH_TOKEN", "")
				UseReleaseTokenSource(nil)
				reached := false
				var authorization string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/moved" {
						reached, authorization = true, r.Header.Get("Authorization")
						http.NotFound(w, r)
						return
					}
					http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
				}))
				t.Cleanup(server.Close)
				routeReleaseAPI(t, server)

				require.Error(t, call(t))
				require.True(t, reached, "the redirect was not followed")
				require.Equal(t, tc.want, authorization)
			})
		}
	}
}

// A name a host gives its source is printed on one line and never carries the
// token: control characters are dropped and the token is redacted.
func TestASourceNameIsSanitisedBeforeItIsPrinted(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	refusingReleaseAPI(t, http.StatusUnauthorized)
	t.Cleanup(func() { UseReleaseTokenSource(nil) })

	UseReleaseTokenSource(func() (string, []string) {
		return "tok-in-name", []string{"login tok-\nin-name\nINJECTED\x1b[31m"}
	})
	for _, lookup := range []func() error{
		func() error { _, err := openAgentRelease(t.Context(), testReleaseURL); return err },
		func() error {
			_, err := githubLatestReleaseTag(t.Context(), "example-org", "service-render")
			return err
		},
	} {
		err := lookup()
		require.ErrorContains(t, err, "the credential from login [redacted]INJECTED[31m")
		require.NotContains(t, err.Error(), "tok-in-name")
		require.NotContains(t, err.Error(), "\n")
		require.NotContains(t, err.Error(), "\x1b")
	}

	// With no token, the sources tried are printed on one line too.
	UseReleaseTokenSource(func() (string, []string) { return "", []string{"GITHUB_TOKEN", "a\nb"} })
	refusingReleaseAPI(t, http.StatusNotFound)
	_, err := openAgentRelease(t.Context(), testReleaseURL)
	require.ErrorContains(t, err, "tried GITHUB_TOKEN, ab")
}

// When a lookup is redirected away from the API and the other host refuses,
// the credential was never sent there, so the refusal does not blame it.
func TestARefusalFromAHostTheTokenWasNotSentToDoesNotBlameTheCredential(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "redirect-secret")
	t.Setenv("GH_TOKEN", "")
	UseReleaseTokenSource(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"refused"}`))
			return
		}
		http.Redirect(w, r, "https://elsewhere.example/moved", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	routeReleaseAPI(t, server)

	for _, lookup := range []func() error{
		func() error { _, err := openAgentRelease(t.Context(), testReleaseURL); return err },
		func() error {
			_, err := githubLatestReleaseTag(t.Context(), "example-org", "service-render")
			return err
		},
	} {
		err := lookup()
		require.ErrorContains(t, err, "refused by https://elsewhere.example after a redirect away from the GitHub API")
		require.NotContains(t, err.Error(), "credential from")
		require.NotContains(t, err.Error(), "redirect-secret")
	}
}
