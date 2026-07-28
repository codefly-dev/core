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
	"sort"
	"strconv"
	"strings"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com/"
	defaultAPIResponseLimit = 8 << 20
	maxGitHubReleasePages   = 100
	maxGitHubErrorBytes     = 64 << 10
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
	if result.save {
		if err := c.store.Save(ctx, result.key, result.entry); err != nil {
			return status, fmt.Errorf("save release cache: %w", err)
		}
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
	key       string
	save      bool
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

	now := time.Now().UTC()
	firstPage := endpoint.String()
	nextPage := firstPage
	visited := map[string]struct{}{firstPage: {}}
	remaining := c.maxResponseBytes
	releases := make([]Release, 0)
	var metadata CacheMetadata

	for page := 0; nextPage != ""; page++ {
		if page >= maxGitHubReleasePages {
			return fetchResult{}, fmt.Errorf("%w: GitHub release pages", ErrDownloadLimit)
		}
		httpRequest, err := c.newReleaseRequest(ctx, nextPage, cached, cachedFound && page == 0)
		if err != nil {
			return fetchResult{}, err
		}
		response, err := c.client.Do(httpRequest)
		if err != nil {
			return fetchResult{}, fmt.Errorf("query GitHub releases: %w", sanitizeURLError(err))
		}

		switch response.StatusCode {
		case http.StatusNotModified:
			_ = response.Body.Close()
			if page != 0 || !cachedFound {
				return fetchResult{}, ErrCacheMiss
			}
			cached.Metadata.ValidatedAt = now
			return fetchResult{entry: cached, fromCache: true, key: key, save: true}, nil

		case http.StatusForbidden, http.StatusTooManyRequests:
			rateErr, limited := githubRateLimit(response, now)
			_ = response.Body.Close()
			if !limited {
				return fetchResult{}, fmt.Errorf("GitHub releases request returned HTTP %d", response.StatusCode)
			}
			if !cachedFound {
				return fetchResult{}, rateErr
			}
			return fetchResult{entry: cached, fromCache: true, stale: true, key: key}, rateErr

		case http.StatusOK:
			pageReleases, used, err := decodeGitHubReleasePage(response.Body, response.ContentLength, remaining)
			if err != nil {
				_ = response.Body.Close()
				return fetchResult{}, err
			}
			remaining -= used
			releases = append(releases, pageReleases...)
			if page == 0 {
				metadata = CacheMetadata{
					ETag:         response.Header.Get("ETag"),
					LastModified: response.Header.Get("Last-Modified"),
					ValidatedAt:  now,
				}
			}
			nextPage, err = nextGitHubPage(response.Header.Get("Link"), httpRequest.URL, visited)
			_ = response.Body.Close()
			if err != nil {
				return fetchResult{}, err
			}

		default:
			status := response.StatusCode
			_ = response.Body.Close()
			return fetchResult{}, fmt.Errorf("GitHub releases request returned HTTP %d", status)
		}
	}

	entry := CacheEntry{Metadata: metadata, Releases: releases}
	return fetchResult{entry: entry, key: key, save: true}, nil
}

func (c *GitHubChecker) newReleaseRequest(ctx context.Context, endpoint string, cached CacheEntry, conditional bool) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, errors.New("create GitHub release request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	if conditional {
		if cached.Metadata.ETag != "" {
			request.Header.Set("If-None-Match", cached.Metadata.ETag)
		}
		if cached.Metadata.LastModified != "" {
			request.Header.Set("If-Modified-Since", cached.Metadata.LastModified)
		}
	}
	return request, nil
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
	if !validAssetName(request.ArtifactName) {
		return Request{}, errors.New("artifact name must be a plain filename")
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
	if err := validateReleaseMetadata(releases); err != nil {
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
	sort.SliceStable(eligible, func(left, right int) bool {
		return eligible[left].Version.Compare(eligible[right].Version) > 0
	})
	release, found, err := selectReleaseAsset(ctx, request, eligible)
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{}, fmt.Errorf("%w for %s/%s", ErrAssetNotFound, request.Platform.OS, request.Platform.Arch)
	}

	comparison := release.Version.Compare(request.Current)
	available := comparison > 0 || (comparison < 0 && request.Downgrade == DowngradeAllow)
	return Status{
		Current:     request.Current,
		Latest:      release.Version,
		Available:   available,
		Channel:     request.Channel,
		Downgrade:   request.Downgrade,
		Release:     release,
		InstallKind: request.InstallKind,
		CheckedAt:   time.Now().UTC(),
		FromCache:   fromCache,
		Stale:       stale,
	}, nil
}

func validateReleaseMetadata(releases []Release) error {
	for index, release := range releases {
		if release.Version.value == "" {
			return fmt.Errorf("%w: release %d has no semantic version", ErrInvalidRelease, index)
		}
	}
	return nil
}

func selectReleaseAsset(ctx context.Context, request Request, releases []Release) (Release, bool, error) {
	for _, release := range releases {
		filtered := release
		filtered.Assets = make([]Asset, 0, len(release.Assets))
		for _, asset := range release.Assets {
			if assetMatchesArtifact(asset.Name, request.ArtifactName, request.Platform.OS) {
				filtered.Assets = append(filtered.Assets, asset)
			}
		}
		if len(filtered.Assets) == 0 {
			continue
		}
		updater, err := selfupdate.NewUpdater(selfupdate.Config{
			Source:        releaseSource{releases: []Release{filtered}},
			OS:            request.Platform.OS,
			Arch:          request.Platform.Arch,
			UniversalArch: "all",
			Prerelease:    request.Channel == ChannelBeta,
		})
		if err != nil {
			return Release{}, false, fmt.Errorf("configure release selection: %w", err)
		}
		selected, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug(request.Repository.Owner, request.Repository.Name))
		if err != nil {
			return Release{}, false, fmt.Errorf("select release: %w", err)
		}
		if !found {
			continue
		}
		for _, candidate := range release.Assets {
			if candidate.ID == selected.AssetID && candidate.Name == selected.AssetName {
				release.Asset = candidate
				release.Asset.Platform = request.Platform
				return release, true, nil
			}
		}
		return Release{}, false, errors.New("selected asset is absent from release metadata")
	}
	return Release{}, false, nil
}

func assetMatchesArtifact(name, artifactName, operatingSystem string) bool {
	if strings.EqualFold(name, artifactName) {
		return true
	}
	if len(name) <= len(artifactName) ||
		!strings.EqualFold(name[:len(artifactName)], artifactName) ||
		(name[len(artifactName)] != '_' && name[len(artifactName)] != '-') {
		return false
	}
	remainder := name[len(artifactName)+1:]
	end := strings.IndexAny(remainder, "_-")
	if end < 0 {
		end = len(remainder)
	}
	token := remainder[:end]
	if strings.EqualFold(token, operatingSystem) {
		return true
	}
	_, err := ParseVersion(token)
	return err == nil
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
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

func decodeGitHubReleasePage(reader io.Reader, contentLength, limit int64) ([]Release, int64, error) {
	if contentLength > limit {
		return nil, 0, ErrDownloadLimit
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read GitHub release response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, 0, ErrDownloadLimit
	}

	var source []githubReleaseJSON
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, 0, fmt.Errorf("decode GitHub release response: %w", err)
	}

	releases := make([]Release, 0, len(source))
	for _, item := range source {
		version, err := ParseVersion(item.TagName)
		if err != nil {
			continue
		}
		assets := make([]Asset, 0, len(item.Assets))
		for _, itemAsset := range item.Assets {
			if itemAsset.ID == 0 || !validAssetName(itemAsset.Name) || itemAsset.URL == "" || itemAsset.Size < 0 || itemAsset.Size > math.MaxInt {
				continue
			}
			assets = append(assets, Asset{
				ID:          itemAsset.ID,
				Name:        itemAsset.Name,
				DownloadURL: itemAsset.URL,
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
	return releases, int64(len(data)), nil
}

func nextGitHubPage(link string, current *url.URL, visited map[string]struct{}) (string, error) {
	if link == "" {
		return "", nil
	}
	for _, item := range strings.Split(link, ",") {
		parts := strings.Split(item, ";")
		nextRelation := false
		for _, parameter := range parts[1:] {
			if strings.TrimSpace(parameter) == `rel="next"` {
				nextRelation = true
				break
			}
		}
		if !nextRelation {
			continue
		}
		raw := strings.TrimSpace(parts[0])
		if len(raw) < 3 || raw[0] != '<' || raw[len(raw)-1] != '>' {
			return "", errors.New("GitHub pagination link is malformed")
		}
		next, err := url.Parse(raw[1 : len(raw)-1])
		if err != nil ||
			next.Scheme != current.Scheme ||
			!strings.EqualFold(next.Host, current.Host) ||
			next.User != nil ||
			next.Fragment != "" ||
			next.Path != current.Path {
			return "", errors.New("GitHub pagination link leaves the release endpoint")
		}
		query := next.Query()
		page, err := strconv.Atoi(query.Get("page"))
		if err != nil || page < 2 || query.Get("per_page") != "100" {
			return "", errors.New("GitHub pagination link has invalid page parameters")
		}
		for key := range query {
			if key != "page" && key != "per_page" {
				return "", errors.New("GitHub pagination link has unexpected parameters")
			}
		}
		value := next.String()
		if _, found := visited[value]; found {
			return "", errors.New("GitHub pagination link contains a cycle")
		}
		visited[value] = struct{}{}
		return value, nil
	}
	return "", nil
}

func githubRateLimit(response *http.Response, now time.Time) (*RateLimitError, bool) {
	limited := response.StatusCode == http.StatusTooManyRequests ||
		response.Header.Get("X-RateLimit-Remaining") == "0" ||
		response.Header.Get("Retry-After") != ""
	if !limited && response.StatusCode == http.StatusForbidden {
		data, _ := io.ReadAll(io.LimitReader(response.Body, maxGitHubErrorBytes+1))
		var body struct {
			Message string `json:"message"`
		}
		if int64(len(data)) <= maxGitHubErrorBytes && json.Unmarshal(data, &body) == nil {
			limited = strings.Contains(strings.ToLower(body.Message), "rate limit")
		}
	}
	if !limited {
		return nil, false
	}
	retryAt := rateLimitReset(response.Header, now)
	if retryAt.IsZero() {
		retryAt = now.Add(time.Minute)
	}
	return &RateLimitError{RetryAt: retryAt}, true
}

func rateLimitReset(header http.Header, now time.Time) time.Time {
	value := header.Get("X-RateLimit-Reset")
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		if retryAfter, retryErr := strconv.ParseInt(header.Get("Retry-After"), 10, 64); retryErr == nil && retryAfter > 0 {
			return now.Add(time.Duration(retryAfter) * time.Second)
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
func (r sourceRelease) GetName() string           { return "" }
func (r sourceRelease) GetURL() string            { return "" }
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
func (a sourceAsset) GetBrowserDownloadURL() string { return a.asset.Name }
