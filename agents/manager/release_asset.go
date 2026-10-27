package manager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-github/v89/github"
)

// ReleaseAsset is an opened loader archive together with the digest that
// establishes its bytes.
type ReleaseAsset struct {
	Body io.ReadCloser
	Size int64
	// Digest is the archive's expected SHA-256 (hex, no prefix) and
	// DigestSource names who computed it: DigestFromChecksums when it comes
	// from the checksums file the release's producer published, which is the
	// only record independent of GitHub; DigestFromGitHub when only GitHub's
	// own record of the upload exists; "" when the release offers nothing to
	// check against, in which case Digest is also empty.
	Digest       string
	DigestSource string
}

// The producers a ReleaseAsset digest can come from.
const (
	DigestFromChecksums = "checksums"
	DigestFromGitHub    = "github"
)

// OpenReleaseAsset opens the exact GitHub release asset named by a loader URL.
// The supplied client authenticates metadata and asset API requests. Redirected
// bytes use the bounded, unauthenticated download client so API credentials never
// travel to the asset CDN. The caller must close the returned reader.
func OpenReleaseAsset(ctx context.Context, releaseURL string, client *github.Client) (io.ReadCloser, int64, error) {
	asset, err := FetchReleaseAsset(ctx, releaseURL, client)
	if err != nil {
		return nil, 0, err
	}
	return asset.Body, asset.Size, nil
}

// FetchReleaseAsset is OpenReleaseAsset with the digest the release records for
// the asset, so a caller that installs the bytes can check them. The caller
// must close Body.
func FetchReleaseAsset(ctx context.Context, releaseURL string, client *github.Client) (*ReleaseAsset, error) {
	u, err := url.Parse(releaseURL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid GitHub release asset URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return nil, fmt.Errorf("invalid GitHub release asset path")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
			return nil, fmt.Errorf("invalid GitHub release asset path")
		}
	}
	if client == nil {
		return nil, fmt.Errorf("GitHub release client is required")
	}
	owner, repo, tag, name := parts[0], parts[1], parts[4], parts[5]
	release, _, err := client.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("resolve agent release: %w", err)
	}
	if release.GetDraft() || release.GetTagName() != tag {
		return nil, fmt.Errorf("agent release is not published at %s", tag)
	}
	var found, checksums *github.ReleaseAsset
	for _, asset := range release.Assets {
		switch {
		case asset.GetName() == name:
			if found != nil {
				return nil, fmt.Errorf("agent release has duplicate asset %s", name)
			}
			found = asset
		case strings.HasSuffix(asset.GetName(), "checksums.txt"):
			if checksums != nil {
				return nil, fmt.Errorf("agent release publishes more than one checksums file (%s, %s); which one records %s is ambiguous", checksums.GetName(), asset.GetName(), name)
			}
			checksums = asset
		}
	}
	if found == nil || found.GetID() <= 0 || found.GetBrowserDownloadURL() != releaseURL {
		return nil, fmt.Errorf("agent release does not contain the requested asset URL")
	}
	digest, source, err := releaseAssetDigest(ctx, client, owner, repo, found, checksums)
	if err != nil {
		return nil, err
	}
	body, _, err := client.Repositories.DownloadReleaseAsset(ctx, owner, repo, found.GetID(), agentDownloadClient)
	if err != nil {
		return nil, fmt.Errorf("open agent release asset: %w", err)
	}
	if body == nil {
		return nil, fmt.Errorf("agent release asset returned no content")
	}
	return &ReleaseAsset{Body: body, Size: int64(found.GetSize()), Digest: digest, DigestSource: source}, nil
}

// releaseAssetDigest prefers the digest the release's producer published over
// the one GitHub recorded: comparing served bytes to GitHub's own record only
// shows GitHub agreeing with itself.
func releaseAssetDigest(ctx context.Context, client *github.Client, owner, repo string, asset, checksums *github.ReleaseAsset) (string, string, error) {
	if checksums != nil {
		body, _, err := client.Repositories.DownloadReleaseAsset(ctx, owner, repo, checksums.GetID(), agentDownloadClient)
		if err != nil {
			return "", "", fmt.Errorf("read the release's checksums: %w", err)
		}
		if body == nil {
			return "", "", fmt.Errorf("release checksums returned no content")
		}
		defer func() { _ = body.Close() }()
		var raw bytes.Buffer
		if _, err := io.Copy(&raw, io.LimitReader(body, (1<<20)+1)); err != nil {
			return "", "", fmt.Errorf("read the release's checksums: %w", err)
		}
		if raw.Len() > 1<<20 {
			return "", "", fmt.Errorf("release checksums exceed 1 MiB")
		}
		entries, err := parseChecksums(raw.Bytes())
		if err != nil {
			return "", "", err
		}
		if digest, ok := entries[asset.GetName()]; ok {
			return digest, DigestFromChecksums, nil
		}
		return "", "", fmt.Errorf("the release's %s records no digest for %s, so the archive cannot be established against its producer's record", checksums.GetName(), asset.GetName())
	}
	if digest := asset.GetDigest(); digest != "" {
		recorded, ok := strings.CutPrefix(digest, "sha256:")
		if !ok || !validSHA256(recorded) {
			return "", "", fmt.Errorf("release asset has an invalid SHA-256 digest")
		}
		return strings.ToLower(recorded), DigestFromGitHub, nil
	}
	return "", "", nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// parseChecksums reads SHA-256 records and refuses malformed or duplicate entries.
func parseChecksums(raw []byte) (map[string]string, error) {
	checksums := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || !validSHA256(fields[0]) {
			return nil, fmt.Errorf("release checksums contain an invalid SHA-256 entry")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("release checksums contain duplicate entry %q", name)
		}
		checksums[name] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read release checksums: %w", err)
	}
	return checksums, nil
}

// openAgentRelease uses the same release metadata and digest discovery for
// anonymous and authenticated downloads. Only credential selection differs.
func openAgentRelease(ctx context.Context, releaseURL string) (*ReleaseAsset, error) {
	asset, err := FetchReleaseAsset(ctx, releaseURL, newGitHubReleaseClient())
	var response *github.ErrorResponse
	if GitHubReleaseToken() == "" && errors.As(err, &response) && response.Response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: the release may be missing, or private; set GITHUB_TOKEN or GH_TOKEN for private agent downloads (a gh CLI login is not read here)", err)
	}
	return asset, err
}
