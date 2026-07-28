package releaseupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mutex sync.Mutex
	key   string
	entry CacheEntry
	found bool
}

func (s *memoryStore) Load(context.Context, string) (CacheEntry, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.entry, s.found, nil
}

func (s *memoryStore) Save(_ context.Context, key string, entry CacheEntry) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.key = key
	s.entry = entry
	s.found = true
	return nil
}

func TestGitHubCheckerRecordedReleaseConditionalCacheAndRateLimit(t *testing.T) {
	recording, err := os.ReadFile("testdata/goreleaser-releases.json")
	require.NoError(t, err)

	var mutex sync.Mutex
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		requestCount++
		require.Equal(t, "/repos/goreleaser/goreleaser/releases", request.URL.Path)
		require.Equal(t, "100", request.URL.Query().Get("per_page"))
		switch requestCount {
		case 1:
			require.Empty(t, request.Header.Get("If-None-Match"))
			response.Header().Set("ETag", `"immutable-recording"`)
			response.Header().Set("Last-Modified", "Sun, 26 Jul 2026 18:04:20 GMT")
			_, err := response.Write(recording)
			require.NoError(t, err)
		case 2:
			require.Equal(t, `"immutable-recording"`, request.Header.Get("If-None-Match"))
			require.Equal(t, "Sun, 26 Jul 2026 18:04:20 GMT", request.Header.Get("If-Modified-Since"))
			response.WriteHeader(http.StatusNotModified)
		case 3:
			require.Equal(t, `"immutable-recording"`, request.Header.Get("If-None-Match"))
			response.Header().Set("X-RateLimit-Remaining", "0")
			response.Header().Set("X-RateLimit-Reset", "1785090000")
			response.WriteHeader(http.StatusForbidden)
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()

	store := &memoryStore{}
	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		Store:      store,
	})
	require.NoError(t, err)

	stable, err := checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:      mustVersion(t, "2.17.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "goreleaser",
		InstallKind:  InstallKindHomebrew,
	})
	require.NoError(t, err)
	require.Equal(t, "2.17.1", stable.Latest.String())
	require.True(t, stable.Available)
	require.Equal(t, "goreleaser_Linux_x86_64.tar.gz", stable.Release.Asset.Name)
	require.Equal(t, "https://api.github.com/repos/goreleaser/goreleaser/releases/assets/490565870", stable.Release.Asset.DownloadURL)
	require.Equal(t, InstallKindHomebrew, stable.InstallKind)
	require.False(t, stable.FromCache)

	beta, err := checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:      mustVersion(t, "2.17.1"),
		Channel:      ChannelBeta,
		Platform:     Platform{OS: "darwin", Arch: "arm64"},
		ArtifactName: "goreleaser",
	})
	require.NoError(t, err)
	require.Equal(t, "2.18.0-83f4c19a-nightly", beta.Latest.String())
	require.True(t, beta.Available)
	require.True(t, beta.Release.Prerelease)
	require.True(t, beta.FromCache)
	require.False(t, beta.Stale)

	cached, err := checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:      mustVersion(t, "2.17.1"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "arm64"},
		ArtifactName: "goreleaser",
	})
	require.ErrorIs(t, err, ErrRateLimited)
	require.Equal(t, "2.17.1", cached.Latest.String())
	require.False(t, cached.Available)
	require.True(t, cached.FromCache)
	require.True(t, cached.Stale)
	var rateErr *RateLimitError
	require.ErrorAs(t, err, &rateErr)
	require.Equal(t, time.Unix(1785090000, 0).UTC(), rateErr.RetryAt)
	require.NotEmpty(t, store.key)
}

func TestGitHubCheckerPaginatesBeforeSelectingStableRelease(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		page := request.URL.Query().Get("page")
		if page == "" {
			releases := make([]githubReleaseJSON, 100)
			for index := range releases {
				releases[index] = githubReleaseJSON{
					ID:         int64(index + 1),
					TagName:    fmt.Sprintf("2.0.%d-beta.1", index),
					Prerelease: true,
					Assets: []githubAssetJSON{{
						ID:   int64(index + 1),
						Name: "tool_linux_amd64",
						URL:  fmt.Sprintf("%s/assets/%d", server.URL, index+1),
						Size: 1,
					}},
				}
			}
			response.Header().Set("Link", fmt.Sprintf(`<%s/repos/owner/repo/releases?page=2&per_page=100>; rel="next"`, server.URL))
			require.NoError(t, json.NewEncoder(response).Encode(releases))
			return
		}
		require.Equal(t, "2", page)
		require.NoError(t, json.NewEncoder(response).Encode([]githubReleaseJSON{{
			ID:      101,
			TagName: "1.1.0",
			Assets: []githubAssetJSON{{
				ID: 101, Name: "tool_linux_amd64", URL: server.URL + "/assets/101", Size: 1,
			}},
		}}))
	}))
	defer server.Close()

	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		Store:      &memoryStore{},
	})
	require.NoError(t, err)
	status, err := checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
	})
	require.NoError(t, err)
	require.Equal(t, "1.1.0", status.Latest.String())
}

func TestGitHubCheckerUsesCacheForHeaderlessSecondaryRateLimit(t *testing.T) {
	cachedRelease := Release{
		ID:      1,
		Version: mustVersion(t, "1.1.0"),
		Assets: []Asset{{
			ID: 1, Name: "tool_linux_amd64", DownloadURL: "https://api.github.com/assets/1", Size: 1,
		}},
	}
	store := &memoryStore{
		entry: CacheEntry{
			Metadata: CacheMetadata{ETag: `"cached"`},
			Releases: []Release{cachedRelease},
		},
		found: true,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
	}))
	defer server.Close()
	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		Store:      store,
	})
	require.NoError(t, err)

	before := time.Now().UTC()
	status, err := checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
	})
	require.ErrorIs(t, err, ErrRateLimited)
	require.True(t, status.Stale)
	var rateErr *RateLimitError
	require.ErrorAs(t, err, &rateErr)
	require.WithinDuration(t, before.Add(time.Minute), rateErr.RetryAt, time.Second)
}

func TestGitHubCheckerDoesNotReplaceSuccessfulCacheWithUnusableResponse(t *testing.T) {
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			response.Header().Set("ETag", `"successful"`)
			_, _ = response.Write([]byte(`[{"id":1,"tag_name":"1.1.0","assets":[{"id":1,"name":"tool_linux_amd64","url":"https://api.github.com/assets/1","size":1}]}]`))
		case 2:
			response.Header().Set("ETag", `"empty"`)
			_, _ = response.Write([]byte(`[]`))
		case 3:
			response.WriteHeader(http.StatusTooManyRequests)
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()
	store := &memoryStore{}
	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		Store:      store,
	})
	require.NoError(t, err)
	request := Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
	}

	_, err = checker.Check(context.Background(), request)
	require.NoError(t, err)
	_, err = checker.Check(context.Background(), request)
	require.ErrorIs(t, err, ErrReleaseNotFound)
	status, err := checker.Check(context.Background(), request)
	require.ErrorIs(t, err, ErrRateLimited)
	require.Equal(t, "1.1.0", status.Latest.String())
	require.True(t, status.Stale)
	require.Equal(t, `"successful"`, store.entry.Metadata.ETag)
}

func TestReleaseSelectionUsesArtifactIdentityBeforeArchitectureFallback(t *testing.T) {
	request := Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "darwin", Arch: "arm64"},
		ArtifactName: "tool",
		Downgrade:    DowngradeDisallow,
	}
	releases := []Release{
		{
			ID:      2,
			Version: mustVersion(t, "2.0.0"),
			Assets: []Asset{
				{ID: 20, Name: "other_darwin_arm64", DownloadURL: "https://example.com/other", Size: 1},
				{ID: 21, Name: "tool_darwin_all", DownloadURL: "https://example.com/tool-all", Size: 1},
			},
		},
		{
			ID:      1,
			Version: mustVersion(t, "1.1.0"),
			Assets: []Asset{{
				ID: 10, Name: "tool_darwin_arm64", DownloadURL: "https://example.com/tool-arm64", Size: 1,
			}},
		},
	}

	status, err := selectStatus(context.Background(), request, releases, false, false)
	require.NoError(t, err)
	require.Equal(t, "2.0.0", status.Latest.String())
	require.Equal(t, "tool_darwin_all", status.Release.Asset.Name)
}

func TestReleaseSelectionDoesNotLogReleaseURLs(t *testing.T) {
	var output bytes.Buffer
	selfupdate.SetLogger(log.New(&output, "", 0))
	defer selfupdate.SetLogger(log.New(io.Discard, "", 0))
	request := Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
		Downgrade:    DowngradeDisallow,
	}
	release := Release{
		ID:      1,
		Version: mustVersion(t, "1.1.0"),
		Name:    "release-name-secret",
		URL:     "https://example.com/release?token=release-secret",
		Assets: []Asset{{
			ID: 1, Name: "tool_linux_amd64", DownloadURL: "https://example.com/tool?token=asset-secret", Size: 1,
		}},
	}

	_, err := selectStatus(context.Background(), request, []Release{release}, false, false)
	require.NoError(t, err)
	require.NotContains(t, output.String(), "release-name-secret")
	require.NotContains(t, output.String(), "release-secret")
	require.NotContains(t, output.String(), "asset-secret")
}

func TestGitHubCheckerRejectsInvalidCachedVersion(t *testing.T) {
	store := &memoryStore{
		entry: CacheEntry{
			Releases: []Release{{
				ID: 1,
				Assets: []Asset{{
					ID: 1, Name: "tool_linux_amd64", DownloadURL: "https://api.github.com/assets/1", Size: 1,
				}},
			}},
		},
		found: true,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		Store:      store,
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, err = checker.Check(context.Background(), Request{
			Repository:   Repository{Owner: "owner", Name: "repo"},
			Current:      mustVersion(t, "1.0.0"),
			Channel:      ChannelStable,
			Platform:     Platform{OS: "linux", Arch: "amd64"},
			ArtifactName: "tool",
		})
	})
	require.ErrorIs(t, err, ErrInvalidRelease)
}

func TestRecordedGoReleaserPlatformSelection(t *testing.T) {
	recording, err := os.Open("testdata/goreleaser-releases.json")
	require.NoError(t, err)
	defer recording.Close()
	releases, _, err := decodeGitHubReleasePage(recording, -1, defaultAPIResponseLimit)
	require.NoError(t, err)

	tests := []struct {
		platform Platform
		name     string
	}{
		{Platform{OS: "darwin", Arch: "amd64"}, "goreleaser_Darwin_x86_64.tar.gz"},
		{Platform{OS: "darwin", Arch: "arm64"}, "goreleaser_Darwin_arm64.tar.gz"},
		{Platform{OS: "linux", Arch: "amd64"}, "goreleaser_Linux_x86_64.tar.gz"},
		{Platform{OS: "linux", Arch: "arm64"}, "goreleaser_Linux_arm64.tar.gz"},
	}
	for _, test := range tests {
		t.Run(test.platform.OS+"-"+test.platform.Arch, func(t *testing.T) {
			status, err := selectStatus(context.Background(), Request{
				Repository:   Repository{Owner: "goreleaser", Name: "goreleaser"},
				Current:      mustVersion(t, "2.17.0"),
				Channel:      ChannelStable,
				Platform:     test.platform,
				ArtifactName: "goreleaser",
			}, releases, false, false)
			require.NoError(t, err)
			require.Equal(t, test.name, status.Release.Asset.Name)
		})
	}
}

func TestRecordedGoReleaserUniversalMacFallback(t *testing.T) {
	recording, err := os.Open("testdata/goreleaser-releases.json")
	require.NoError(t, err)
	defer recording.Close()
	releases, _, err := decodeGitHubReleasePage(recording, -1, defaultAPIResponseLimit)
	require.NoError(t, err)

	stable := releases[1]
	filtered := stable.Assets[:0]
	for _, asset := range stable.Assets {
		if asset.Name != "goreleaser_Darwin_arm64.tar.gz" {
			filtered = append(filtered, asset)
		}
	}
	stable.Assets = filtered

	status, err := selectStatus(context.Background(), Request{
		Repository:   Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:      mustVersion(t, "2.17.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "darwin", Arch: "arm64"},
		ArtifactName: "goreleaser",
	}, []Release{stable}, false, false)
	require.NoError(t, err)
	require.Equal(t, "goreleaser_Darwin_all.tar.gz", status.Release.Asset.Name)
}

func TestStatusRequiresExplicitDowngradePolicy(t *testing.T) {
	release := Release{
		ID:      1,
		Version: mustVersion(t, "1.0.0"),
		Assets: []Asset{{
			ID: 1, Name: "tool_linux_amd64", DownloadURL: "https://example.com/tool", Size: 1,
		}},
	}
	request := Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "2.0.0"),
		Channel:      ChannelStable,
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
	}
	status, err := selectStatus(context.Background(), request, []Release{release}, false, false)
	require.NoError(t, err)
	require.False(t, status.Available)

	request.Downgrade = DowngradeAllow
	status, err = selectStatus(context.Background(), request, []Release{release}, false, false)
	require.NoError(t, err)
	require.True(t, status.Available)
}

func TestGitHubCheckerRejectsResponseOverLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", "2")
		_, _ = response.Write([]byte("[]"))
	}))
	defer server.Close()
	checker, err := NewGitHubChecker(GitHubOptions{
		HTTPClient:       server.Client(),
		APIBaseURL:       server.URL,
		Store:            &memoryStore{},
		MaxResponseBytes: 1,
	})
	require.NoError(t, err)
	_, err = checker.Check(context.Background(), Request{
		Repository:   Repository{Owner: "owner", Name: "repo"},
		Current:      mustVersion(t, "1.0.0"),
		Platform:     Platform{OS: "linux", Arch: "amd64"},
		ArtifactName: "tool",
	})
	require.ErrorIs(t, err, ErrDownloadLimit)
}

func mustVersion(t *testing.T, value string) Version {
	t.Helper()
	version, err := ParseVersion(value)
	require.NoError(t, err)
	return version
}
