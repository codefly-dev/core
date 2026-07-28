package releaseupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com/"
	defaultAPIResponseLimit = 8 << 20
)

// GitHubOptions configures release discovery. Store is required; Token is
// optional and is never included in returned errors.
type GitHubOptions struct {
	HTTPClient       *http.Client
	APIBaseURL       string
	Token            string
	UserAgent        string
	Store            Store
	MaxResponseBytes int64
}

// GitHubChecker discovers releases through GitHub's releases API.
type GitHubChecker struct {
	client           *http.Client
	baseURL          *url.URL
	token            string
	userAgent        string
	store            Store
	maxResponseBytes int64
}

// NewGitHubChecker constructs a GitHub release checker.
func NewGitHubChecker(options GitHubOptions) (*GitHubChecker, error) {
	if options.Store == nil {
		return nil, errors.New("release cache store is required")
	}

	base := options.APIBaseURL
	if base == "" {
		base = defaultGitHubAPIBaseURL
	}
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("GitHub API base URL must be an HTTPS origin without credentials, query, or fragment")
	}
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}

	client := http.DefaultClient
	if options.HTTPClient != nil {
		client = options.HTTPClient
	}
	clientCopy := *client
	originalRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if originalRedirect != nil {
			if err := originalRedirect(request, via); err != nil {
				return err
			}
		}
		return errors.New("GitHub API redirect refused")
	}

	limit := options.MaxResponseBytes
	if limit == 0 {
		limit = defaultAPIResponseLimit
	}
	if limit < 1 {
		return nil, errors.New("GitHub API response limit must be positive")
	}

	userAgent := options.UserAgent
	if userAgent == "" {
		userAgent = "codefly-core-releaseupdate"
	}

	return &GitHubChecker{
		client:           &clientCopy,
		baseURL:          baseURL,
		token:            options.Token,
		userAgent:        userAgent,
		store:            options.Store,
		maxResponseBytes: limit,
	}, nil
}

// Check performs release discovery and never mutates an installation.
func (c *GitHubChecker) Check(ctx context.Context, request Request) (Status, error) {
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Status{}, err
	}

	result, fetchErr := c.fetch(ctx, normalized.Repository)
	if len(result.entry.Releases) == 0 && fetchErr != nil {
		return Status{}, fetchErr
	}

	status, err := selectStatus(ctx, normalized, result.entry.Releases, result.fromCache, result.stale)
	if err != nil {
		return Status{}, err
	}
	if fetchErr != nil {
		return status, fetchErr
	}
	return status, nil
}

type fetchResult struct {
	entry     CacheEntry
	fromCache bool
	stale     bool
}

func (c *GitHubChecker) fetch(ctx context.Context, repository Repository) (fetchResult, error) {
	key := c.cacheKey(repository)
	cached, cachedFound, err := c.store.Load(ctx, key)
	if err != nil {
		return fetchResult{}, fmt.Errorf("load release cache: %w", err)
	}

	endpoint := *c.baseURL
	endpoint.Path = path.Join(c.baseURL.Path, "repos", repository.Owner, repository.Name, "releases")
	query := endpoint.Query()
	query.Set("per_page", "100")
	endpoint.RawQuery = query.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return fetchResult{}, errors.New("create GitHub release request")
	}
	httpRequest.Header.Set("Accept", "application/vnd.github+json")
	httpRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	httpRequest.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	}
	if cachedFound {
		if cached.Metadata.ETag != "" {
			httpRequest.Header.Set("If-None-Match", cached.Metadata.ETag)
		}
		if cached.Metadata.LastModified != "" {
			httpRequest.Header.Set("If-Modified-Since", cached.Metadata.LastModified)
		}
	}

	response, err := c.client.Do(httpRequest)
	if err != nil {
		return fetchResult{}, fmt.Errorf("query GitHub releases: %w", sanitizeURLError(err))
	}
	defer response.Body.Close()

	now := time.Now().UTC()
	switch response.StatusCode {
	case http.StatusNotModified:
		if !cachedFound {
			return fetchResult{}, ErrCacheMiss
		}
		cached.Metadata.ValidatedAt = now
		if err := c.store.Save(ctx, key, cached); err != nil {
			return fetchResult{entry: cached, fromCache: true}, fmt.Errorf("save release cache: %w", err)
		}
		return fetchResult{entry: cached, fromCache: true}, nil

	case http.StatusForbidden:
		if response.Header.Get("X-RateLimit-Remaining") != "0" && response.Header.Get("Retry-After") == "" {
			return fetchResult{}, errors.New("GitHub releases request returned HTTP 403")
		}
		fallthrough
	case http.StatusTooManyRequests:
		rateErr := &RateLimitError{RetryAt: rateLimitReset(response.Header)}
		if !cachedFound {
			return fetchResult{}, rateErr
		}
		return fetchResult{entry: cached, fromCache: true, stale: true}, rateErr

	case http.StatusOK:
		releases, err := decodeGitHubReleases(response.Body, response.ContentLength, c.maxResponseBytes)
		if err != nil {
			return fetchResult{}, err
		}
		entry := CacheEntry{
			Metadata: CacheMetadata{
				ETag:         response.Header.Get("ETag"),
				LastModified: response.Header.Get("Last-Modified"),
				ValidatedAt:  now,
			},
			Releases: releases,
		}
		if err := c.store.Save(ctx, key, entry); err != nil {
			return fetchResult{entry: entry}, fmt.Errorf("save release cache: %w", err)
		}
		return fetchResult{entry: entry}, nil

	default:
		return fetchResult{}, fmt.Errorf("GitHub releases request returned HTTP %d", response.StatusCode)
	}
}

func (c *GitHubChecker) cacheKey(repository Repository) string {
	return "github:" + c.baseURL.Host + strings.TrimSuffix(c.baseURL.Path, "/") + ":" + repository.Owner + "/" + repository.Name
}

func normalizeRequest(request Request) (Request, error) {
	if err := validateRepository(request.Repository); err != nil {
		return Request{}, err
	}
	if request.Current.value == "" {
		return Request{}, errors.New("current semantic version is required")
	}
	if request.Channel == "" {
		request.Channel = ChannelStable
	}
	if request.Channel != ChannelStable && request.Channel != ChannelBeta {
		return Request{}, fmt.Errorf("%w: %q", ErrUnsupportedChannel, request.Channel)
	}
	if request.Platform == (Platform{}) {
		request.Platform = CurrentPlatform()
	}
	if request.Platform.OS == "" || request.Platform.Arch == "" {
		return Request{}, ErrUnsupportedPlatform
	}
	if request.InstallKind == "" {
		request.InstallKind = InstallKindUnknown
	}
	switch request.InstallKind {
	case InstallKindUnknown, InstallKindDirect, InstallKindHomebrew, InstallKindTauri, InstallKindApplicationBundle, InstallKindManaged:
	default:
		return Request{}, fmt.Errorf("unsupported install kind %q", request.InstallKind)
	}
	if request.Downgrade == "" {
		request.Downgrade = DowngradeDisallow
	}
	if request.Downgrade != DowngradeDisallow && request.Downgrade != DowngradeAllow {
		return Request{}, fmt.Errorf("unsupported downgrade policy %q", request.Downgrade)
	}
	return request, nil
}

func validateRepository(repository Repository) error {
	for label, value := range map[string]string{"owner": repository.Owner, "name": repository.Name} {
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00\r\n") {
			return fmt.Errorf("invalid repository %s", label)
		}
	}
	return nil
}

func selectStatus(ctx context.Context, request Request, releases []Release, fromCache, stale bool) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	eligible := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.Draft {
			continue
		}
		if request.Channel == ChannelStable && (release.Prerelease || release.Version.prerelease()) {
			continue
		}
		eligible = append(eligible, release)
	}
	if len(eligible) == 0 {
		return Status{}, ErrReleaseNotFound
	}

	source := releaseSource{releases: eligible}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:        source,
		OS:            request.Platform.OS,
		Arch:          request.Platform.Arch,
		UniversalArch: "all",
		Prerelease:    request.Channel == ChannelBeta,
	})
	if err != nil {
		return Status{}, fmt.Errorf("configure release selection: %w", err)
	}
	selected, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug(request.Repository.Owner, request.Repository.Name))
	if err != nil {
		return Status{}, fmt.Errorf("select release: %w", err)
	}
	if !found {
		return Status{}, fmt.Errorf("%w for %s/%s", ErrAssetNotFound, request.Platform.OS, request.Platform.Arch)
	}

	var release Release
	matched := false
	for _, candidate := range eligible {
		if candidate.ID == selected.ReleaseID {
			release = candidate
			matched = true
			break
		}
	}
	if !matched {
		return Status{}, errors.New("selected release is absent from source metadata")
	}

	matched = false
	for _, candidate := range release.Assets {
		if candidate.ID == selected.AssetID && candidate.Name == selected.AssetName {
			release.Asset = candidate
			release.Asset.Platform = request.Platform
			matched = true
			break
		}
		matched = false
	}
	if !matched {
		return Status{}, errors.New("selected asset is absent from release metadata")
	}

	comparison := release.Version.Compare(request.Current)
	available := comparison > 0 || (comparison < 0 && request.Downgrade == DowngradeAllow)
	return Status{
		Current:     request.Current,
		Latest:      release.Version,
		Available:   available,
		Channel:     request.Channel,
		Release:     release,
		InstallKind: request.InstallKind,
		CheckedAt:   time.Now().UTC(),
		FromCache:   fromCache,
		Stale:       stale,
	}, nil
}

type githubReleaseJSON struct {
	ID          int64             `json:"id"`
	TagName     string            `json:"tag_name"`
	Name        string            `json:"name"`
	HTMLURL     string            `json:"html_url"`
	PublishedAt time.Time         `json:"published_at"`
	Prerelease  bool              `json:"prerelease"`
	Draft       bool              `json:"draft"`
	Assets      []githubAssetJSON `json:"assets"`
}

type githubAssetJSON struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func decodeGitHubReleases(reader io.Reader, contentLength, limit int64) ([]Release, error) {
	if contentLength > limit {
		return nil, ErrDownloadLimit
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub release response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrDownloadLimit
	}

	var source []githubReleaseJSON
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("decode GitHub release response: %w", err)
	}

	releases := make([]Release, 0, len(source))
	for _, item := range source {
		version, err := ParseVersion(item.TagName)
		if err != nil {
			continue
		}
		assets := make([]Asset, 0, len(item.Assets))
		for _, itemAsset := range item.Assets {
			if itemAsset.ID == 0 || itemAsset.Name == "" || itemAsset.BrowserDownloadURL == "" || itemAsset.Size < 0 || itemAsset.Size > math.MaxInt {
				continue
			}
			assets = append(assets, Asset{
				ID:          itemAsset.ID,
				Name:        itemAsset.Name,
				DownloadURL: itemAsset.BrowserDownloadURL,
				Size:        itemAsset.Size,
			})
		}
		releases = append(releases, Release{
			ID:          item.ID,
			Version:     version,
			Name:        item.Name,
			URL:         item.HTMLURL,
			PublishedAt: item.PublishedAt,
			Prerelease:  item.Prerelease,
			Draft:       item.Draft,
			Assets:      assets,
		})
	}
	return releases, nil
}

func rateLimitReset(header http.Header) time.Time {
	value := header.Get("X-RateLimit-Reset")
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		if retryAfter, retryErr := strconv.ParseInt(header.Get("Retry-After"), 10, 64); retryErr == nil && retryAfter > 0 {
			return time.Now().UTC().Add(time.Duration(retryAfter) * time.Second)
		}
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func sanitizeURLError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return urlError.Err
	}
	return err
}

type releaseSource struct {
	releases []Release
}

func (s releaseSource) ListReleases(context.Context, selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	releases := make([]selfupdate.SourceRelease, 0, len(s.releases))
	for index := range s.releases {
		releases = append(releases, sourceRelease{release: &s.releases[index]})
	}
	return releases, nil
}

func (releaseSource) DownloadReleaseAsset(context.Context, *selfupdate.Release, int64) (io.ReadCloser, error) {
	return nil, errors.New("release selection source does not download assets")
}

type sourceRelease struct {
	release *Release
}

func (r sourceRelease) GetID() int64              { return r.release.ID }
func (r sourceRelease) GetTagName() string        { return r.release.Version.String() }
func (r sourceRelease) GetDraft() bool            { return r.release.Draft }
func (r sourceRelease) GetPrerelease() bool       { return r.release.Prerelease }
func (r sourceRelease) GetPublishedAt() time.Time { return r.release.PublishedAt }
func (r sourceRelease) GetReleaseNotes() string   { return "" }
func (r sourceRelease) GetName() string           { return r.release.Name }
func (r sourceRelease) GetURL() string            { return r.release.URL }
func (r sourceRelease) GetAssets() []selfupdate.SourceAsset {
	assets := make([]selfupdate.SourceAsset, 0, len(r.release.Assets))
	for index := range r.release.Assets {
		assets = append(assets, sourceAsset{asset: &r.release.Assets[index]})
	}
	return assets
}

type sourceAsset struct {
	asset *Asset
}

func (a sourceAsset) GetID() int64                  { return a.asset.ID }
func (a sourceAsset) GetName() string               { return a.asset.Name }
func (a sourceAsset) GetSize() int                  { return int(a.asset.Size) }
func (a sourceAsset) GetBrowserDownloadURL() string { return a.asset.DownloadURL }
