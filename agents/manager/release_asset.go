package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-github/v89/github"
)

// OpenReleaseAsset opens the exact GitHub release asset named by a loader URL.
// The supplied client authenticates metadata and asset API requests. Redirected
// bytes use the bounded, unauthenticated download client so API credentials never
// travel to the asset CDN. The caller must close the returned reader.
func OpenReleaseAsset(ctx context.Context, releaseURL string, client *github.Client) (io.ReadCloser, int64, error) {
	u, err := url.Parse(releaseURL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, 0, fmt.Errorf("invalid GitHub release asset URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return nil, 0, fmt.Errorf("invalid GitHub release asset path")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
			return nil, 0, fmt.Errorf("invalid GitHub release asset path")
		}
	}
	if client == nil {
		return nil, 0, fmt.Errorf("GitHub release client is required")
	}
	owner, repo, tag, name := parts[0], parts[1], parts[4], parts[5]
	release, _, err := client.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve agent release: %w", err)
	}
	if release.GetDraft() || release.GetTagName() != tag {
		return nil, 0, fmt.Errorf("agent release is not published at %s", tag)
	}
	var found *github.ReleaseAsset
	for _, asset := range release.Assets {
		if asset.GetName() != name {
			continue
		}
		if found != nil {
			return nil, 0, fmt.Errorf("agent release has duplicate asset %s", name)
		}
		found = asset
	}
	if found == nil || found.GetID() <= 0 || found.GetBrowserDownloadURL() != releaseURL {
		return nil, 0, fmt.Errorf("agent release does not contain the requested asset URL")
	}
	body, _, err := client.Repositories.DownloadReleaseAsset(ctx, owner, repo, found.GetID(), agentDownloadClient)
	if err != nil {
		return nil, 0, fmt.Errorf("open agent release asset: %w", err)
	}
	if body == nil {
		return nil, 0, fmt.Errorf("agent release asset returned no content")
	}
	return body, int64(found.GetSize()), nil
}

func openAgentRelease(ctx context.Context, releaseURL string) (io.ReadCloser, int64, error) {
	if githubReleaseToken() != "" {
		return OpenReleaseAsset(ctx, releaseURL, newGitHubReleaseClient())
	}
	resp, err := fetchRelease(ctx, releaseURL)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("unexpected status code %d when downloading agent", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}
