package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v89/github"
	"github.com/stretchr/testify/require"
)

const testReleaseURL = "https://github.com/example-org/service-render/releases/download/v0.1.0/service-render_0.1.0_linux_amd64.tar.gz"

func TestOpenReleaseAssetAuthenticatesAPIWithoutSendingCredentialsToCDN(t *testing.T) {
	var apiCalls, cdnCalls atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnCalls.Add(1)
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "credential leaked to CDN", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, "archive")
	}))
	t.Cleanup(cdn.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/repos/example-org/service-render/releases/tags/v0.1.0":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"v0.1.0","assets":[{"id":12,"name":"service-render_0.1.0_linux_amd64.tar.gz","size":7,"browser_download_url":%q}]}`, testReleaseURL)
		case "/repos/example-org/service-render/releases/assets/12":
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "binary accept header required", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, cdn.URL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	endpoint := api.URL + "/"
	client, err := github.NewClient(github.WithURLs(&endpoint, &endpoint), github.WithAuthToken("test-token"))
	require.NoError(t, err)
	body, size, err := OpenReleaseAsset(t.Context(), testReleaseURL, client)
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "archive", string(data))
	require.EqualValues(t, 7, size)
	require.EqualValues(t, 2, apiCalls.Load())
	require.EqualValues(t, 1, cdnCalls.Load())

	anonymous, err := github.NewClient(github.WithURLs(&endpoint, &endpoint))
	require.NoError(t, err)
	_, _, err = OpenReleaseAsset(t.Context(), testReleaseURL, anonymous)
	require.ErrorContains(t, err, "404")
}

func TestOpenReleaseAssetRejectsWrongPublicationIdentityBeforeReadingBytes(t *testing.T) {
	for _, scenario := range []string{"draft", "tag", "missing", "duplicate", "url", "id"} {
		t.Run(scenario, func(t *testing.T) {
			asset := map[string]any{"id": 12, "name": "service-render_0.1.0_linux_amd64.tar.gz", "browser_download_url": testReleaseURL}
			assets := []any{asset}
			release := map[string]any{"tag_name": "v0.1.0", "assets": assets}
			switch scenario {
			case "draft":
				release["draft"] = true
			case "tag":
				release["tag_name"] = "v0.2.0"
			case "missing":
				release["assets"] = []any{}
			case "duplicate":
				release["assets"] = append(assets, asset)
			case "url":
				asset["browser_download_url"] = "https://example.invalid/elsewhere"
			case "id":
				asset["id"] = 0
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/releases/tags/") {
					t.Error("must not request asset bytes for a mismatched publication")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(release)
			}))
			t.Cleanup(api.Close)
			endpoint := api.URL + "/"
			client, err := github.NewClient(github.WithURLs(&endpoint, &endpoint))
			require.NoError(t, err)
			_, _, err = OpenReleaseAsset(t.Context(), testReleaseURL, client)
			require.Error(t, err)
		})
	}
}

func TestOpenReleaseAssetRefusesUntrustedURLAndCancelledContext(t *testing.T) {
	for _, target := range []string{"http://github.com/o/r/releases/download/v1/a", "https://github.com.evil.invalid/o/r/releases/download/v1/a", "https://user@github.com/o/r/releases/download/v1/a", "https://github.com/o/r/releases/download/v1/a?token=x", "https://github.com/o/r/releases/download/v1/a#fragment", "https://github.com/o/r/releases/download/v1/../a", "https://github.com/o/r/releases/download/v1/a%2Fb", "https://github.com/o/r/other/v1/a"} {
		_, _, err := OpenReleaseAsset(t.Context(), target, nil)
		require.ErrorContains(t, err, "invalid GitHub release asset")
	}
	client, err := github.NewClient()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = OpenReleaseAsset(ctx, testReleaseURL, client)
	require.ErrorIs(t, err, context.Canceled)
}

// TestFetchReleaseAssetPrefersTheProducersChecksums establishes the digest
// source order: the checksums file the release's producer published outranks
// GitHub's own record of the upload, and a release with neither yields nothing
// to hold the bytes to rather than an invented digest.
func TestFetchReleaseAssetPrefersTheProducersChecksums(t *testing.T) {
	const archive = "service-render_0.1.0_linux_amd64.tar.gz"
	for _, scenario := range []struct {
		name, wantDigest, wantSource string
		checksums                    bool
		githubDigest                 string
	}{
		{name: "checksums outrank github", checksums: true, githubDigest: "sha256:" + strings.Repeat("b", 64), wantDigest: strings.Repeat("a", 64), wantSource: DigestFromChecksums},
		{name: "github record alone", githubDigest: "sha256:" + strings.Repeat("b", 64), wantDigest: strings.Repeat("b", 64), wantSource: DigestFromGitHub},
		{name: "nothing to hold to"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assets := []map[string]any{{"id": 12, "name": archive, "size": 7, "browser_download_url": testReleaseURL, "digest": scenario.githubDigest}}
			if scenario.checksums {
				assets = append(assets, map[string]any{"id": 13, "name": "service-render_0.1.0_checksums.txt", "size": 80, "browser_download_url": strings.Replace(testReleaseURL, archive, "service-render_0.1.0_checksums.txt", 1)})
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/example-org/service-render/releases/tags/v0.1.0":
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.0", "assets": assets})
				case "/repos/example-org/service-render/releases/assets/13":
					fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", strings.Repeat("a", 64), archive, strings.Repeat("c", 64))
				case "/repos/example-org/service-render/releases/assets/12":
					fmt.Fprint(w, "archive")
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(api.Close)
			endpoint := api.URL + "/"
			client, err := github.NewClient(github.WithURLs(&endpoint, &endpoint))
			require.NoError(t, err)
			asset, err := FetchReleaseAsset(t.Context(), testReleaseURL, client)
			require.NoError(t, err)
			defer asset.Body.Close()
			require.Equal(t, scenario.wantDigest, asset.Digest)
			require.Equal(t, scenario.wantSource, asset.DigestSource)
		})
	}
}

// TestFetchReleaseAssetRefusesAChecksumsFileThatOmitsTheArchive: a producer's
// record that exists but says nothing about the archive is a verdict, not an
// absence — the bytes cannot be established, so they are not served.
func TestFetchReleaseAssetRefusesAChecksumsFileThatOmitsTheArchive(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example-org/service-render/releases/tags/v0.1.0":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"v0.1.0","assets":[{"id":12,"name":"service-render_0.1.0_linux_amd64.tar.gz","size":7,"browser_download_url":%q},{"id":13,"name":"checksums.txt","size":10,"browser_download_url":"https://github.com/example-org/service-render/releases/download/v0.1.0/checksums.txt"}]}`, testReleaseURL)
		case "/repos/example-org/service-render/releases/assets/13":
			fmt.Fprintf(w, "%s  other.tar.gz\n", strings.Repeat("c", 64))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(api.Close)
	endpoint := api.URL + "/"
	client, err := github.NewClient(github.WithURLs(&endpoint, &endpoint))
	require.NoError(t, err)
	_, err = FetchReleaseAsset(t.Context(), testReleaseURL, client)
	require.ErrorContains(t, err, "records no digest for service-render_0.1.0_linux_amd64.tar.gz")
}

func TestParseChecksumsReadsSha256sumOutput(t *testing.T) {
	parsed := parseChecksums([]byte("ABCD  a.tar.gz\n0123 *b.tar.gz\n\nmalformed line here\n"))
	require.Equal(t, map[string]string{"a.tar.gz": "abcd", "b.tar.gz": "0123"}, parsed)
}
