package manager

import (
	"bufio"
	"bytes"
	"context"
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
		defer func() { _ = body.Close() }()
		var raw bytes.Buffer
		if _, err := io.Copy(&raw, io.LimitReader(body, 1<<20)); err != nil {
			return "", "", fmt.Errorf("read the release's checksums: %w", err)
		}
		if digest, ok := parseChecksums(raw.Bytes())[asset.GetName()]; ok {
			return digest, DigestFromChecksums, nil
		}
		return "", "", fmt.Errorf("the release's %s records no digest for %s, so the archive cannot be established against its producer's record", checksums.GetName(), asset.GetName())
	}
	if recorded, ok := strings.CutPrefix(asset.GetDigest(), "sha256:"); ok && recorded != "" {
		return recorded, DigestFromGitHub, nil
	}
	return "", "", nil
}

// parseChecksums reads `<hex>  <name>` lines; sha256sum's binary-mode `*`
// marker before the name is stripped.
func parseChecksums(raw []byte) map[string]string {
	checksums := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) == 2 {
			checksums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return checksums
}

// openAgentRelease opens a loader archive with the credential GitHubReleaseToken
// resolves. Without one the asset is fetched anonymously, which a private
// repository answers with 404; that answer names the rule, because a 404 here
// has cost a hosted build an evening before the credential was suspected.
func openAgentRelease(ctx context.Context, releaseURL string) (*ReleaseAsset, error) {
	if githubReleaseToken() != "" {
		return FetchReleaseAsset(ctx, releaseURL, newGitHubReleaseClient())
	}
	resp, err := fetchRelease(ctx, releaseURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("unexpected status code 404 when downloading agent: no GITHUB_TOKEN or GH_TOKEN is set, and a private repository's release reads as missing without one (a gh CLI login is not read here)")
		}
		return nil, fmt.Errorf("unexpected status code %d when downloading agent", resp.StatusCode)
	}
	asset := &ReleaseAsset{Body: resp.Body, Size: resp.ContentLength}
	// A public release's producer may have published a checksums file beside
	// the archive; it is the only digest available on this path.
	if digest, ok := anonymousChecksum(ctx, releaseURL); ok {
		asset.Digest, asset.DigestSource = digest, DigestFromChecksums
	}
	return asset, nil
}

// anonymousChecksum reads `checksums.txt` beside an anonymously fetched asset.
// Absence is not an error: nothing lists a public release's assets without the
// API, so the one name `codefly publish` writes is the one tried.
func anonymousChecksum(ctx context.Context, releaseURL string) (string, bool) {
	dir, name := releaseURL[:strings.LastIndex(releaseURL, "/")+1], releaseURL[strings.LastIndex(releaseURL, "/")+1:]
	resp, err := fetchRelease(ctx, dir+"checksums.txt")
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var raw bytes.Buffer
	if _, err := io.Copy(&raw, io.LimitReader(resp.Body, 1<<20)); err != nil {
		return "", false
	}
	digest, ok := parseChecksums(raw.Bytes())[name]
	return digest, ok
}
