package releaseupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

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
		Repository:  Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:     mustVersion(t, "2.17.0"),
		Channel:     ChannelStable,
		Platform:    Platform{OS: "linux", Arch: "amd64"},
		InstallKind: InstallKindHomebrew,
	})
	require.NoError(t, err)
	require.Equal(t, "2.17.1", stable.Latest.String())
	require.True(t, stable.Available)
	require.Equal(t, "goreleaser_Linux_x86_64.tar.gz", stable.Release.Asset.Name)
	require.Equal(t, InstallKindHomebrew, stable.InstallKind)
	require.False(t, stable.FromCache)

	beta, err := checker.Check(context.Background(), Request{
		Repository: Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:    mustVersion(t, "2.17.1"),
		Channel:    ChannelBeta,
		Platform:   Platform{OS: "darwin", Arch: "arm64"},
	})
	require.NoError(t, err)
	require.Equal(t, "2.18.0-83f4c19a-nightly", beta.Latest.String())
	require.True(t, beta.Available)
	require.True(t, beta.Release.Prerelease)
	require.True(t, beta.FromCache)
	require.False(t, beta.Stale)

	cached, err := checker.Check(context.Background(), Request{
		Repository: Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:    mustVersion(t, "2.17.1"),
		Channel:    ChannelStable,
		Platform:   Platform{OS: "linux", Arch: "arm64"},
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

func TestRecordedGoReleaserPlatformSelection(t *testing.T) {
	recording, err := os.Open("testdata/goreleaser-releases.json")
	require.NoError(t, err)
	defer recording.Close()
	releases, err := decodeGitHubReleases(recording, -1, defaultAPIResponseLimit)
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
				Repository: Repository{Owner: "goreleaser", Name: "goreleaser"},
				Current:    mustVersion(t, "2.17.0"),
				Channel:    ChannelStable,
				Platform:   test.platform,
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
	releases, err := decodeGitHubReleases(recording, -1, defaultAPIResponseLimit)
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
		Repository: Repository{Owner: "goreleaser", Name: "goreleaser"},
		Current:    mustVersion(t, "2.17.0"),
		Channel:    ChannelStable,
		Platform:   Platform{OS: "darwin", Arch: "arm64"},
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
		Repository: Repository{Owner: "owner", Name: "repo"},
		Current:    mustVersion(t, "2.0.0"),
		Channel:    ChannelStable,
		Platform:   Platform{OS: "linux", Arch: "amd64"},
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
		Repository: Repository{Owner: "owner", Name: "repo"},
		Current:    mustVersion(t, "1.0.0"),
		Platform:   Platform{OS: "linux", Arch: "amd64"},
	})
	require.ErrorIs(t, err, ErrDownloadLimit)
}

func mustVersion(t *testing.T, value string) Version {
	t.Helper()
	version, err := ParseVersion(value)
	require.NoError(t, err)
	return version
}
