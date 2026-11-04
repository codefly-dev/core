package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// responseLimit bounds each proxy response. @v/list for core is ~7KB at 461
// versions, so this is three orders of magnitude of headroom and still a
// bound: an unbounded read against a URL taken from the environment is how a
// gate becomes a way to exhaust a runner.
const responseLimit = 8 << 20

// requestTimeout bounds the whole catalogue fetch. A gate that hangs is worse
// than one that fails: it holds the merge at "Expected" with nothing to read.
const requestTimeout = 60 * time.Second

// catalog is what core has published, as the module proxy reports it.
//
// The proxy is the authority here rather than GitHub's releases API because
// the question a pin asks is "can this version resolve", and @v/list answers
// exactly that for all 461 of core's versions, where GitHub Releases exist for
// only the most recent 62. It also needs no credential, which keeps this gate
// out of the credential model in docs/ci-credentials.md entirely.
type catalog struct {
	client *http.Client
	base   *url.URL

	// stable is every published version that is not a prerelease and not a
	// pseudo-version, ascending. Those are the versions a consumer is expected
	// to be on, so they are what "behind" counts and what the clock reads.
	stable []string
	// published is every version @v/list reported, for membership tests: a
	// plain pin that is not in here is a pin core never published.
	published map[string]bool

	// times caches @v/<version>.info lookups. The oldest missing version is
	// usually the same one for every module in a repository, so this is
	// normally one request for the whole run.
	times map[string]time.Time
}

// proxyBase resolves which proxy to ask: the explicit flag, else the first
// usable GOPROXY entry, else the public proxy. GOPROXY is honoured rather than
// hardcoded because it is how the consumer's own build resolves core — a gate
// answering from a different source than the build is not a verdict on the
// build.
func proxyBase(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	setting := os.Getenv("GOPROXY")
	if setting == "" {
		return "https://proxy.golang.org", nil
	}
	// GOPROXY is a list separated by comma (fall back on error) or pipe (fall
	// back on any error). "direct" and "off" are not proxies.
	for _, entry := range strings.FieldsFunc(setting, func(r rune) bool { return r == ',' || r == '|' }) {
		entry = strings.TrimSpace(entry)
		switch entry {
		case "", "direct", "off", "noproxy":
			continue
		}
		if strings.HasPrefix(entry, "https://") || strings.HasPrefix(entry, "http://") {
			return entry, nil
		}
	}
	return "", errNoProxy
}

// newCatalog fetches the version list once.
func newCatalog(ctx context.Context, explicitProxy string) (*catalog, error) {
	raw, err := proxyBase(explicitProxy)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimSuffix(raw, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("parse module proxy URL: %w", err)
	}

	built := &catalog{
		client:    &http.Client{Timeout: requestTimeout},
		base:      base,
		published: map[string]bool{},
		times:     map[string]time.Time{},
	}

	body, err := built.get(ctx, "@v/list")
	if err != nil {
		return nil, fmt.Errorf("list %s versions: %w", corePath, err)
	}
	for _, line := range strings.Fields(string(body)) {
		if !semver.IsValid(line) {
			continue
		}
		built.published[line] = true
		if semver.Prerelease(line) == "" {
			built.stable = append(built.stable, line)
		}
	}
	if len(built.stable) == 0 {
		return nil, fmt.Errorf(
			"the module proxy at %s reported no published versions of %s.\n"+
				"Refusing to report every pin up to date against an empty catalogue",
			base, corePath)
	}
	sort.Slice(built.stable, func(i, j int) bool {
		return semver.Compare(built.stable[i], built.stable[j]) < 0
	})
	return built, nil
}

// get fetches one proxy path. notFound is returned for a 404/410, which the
// proxy uses for "no such version" — a real answer, not a failure.
var errNotFound = errors.New("not found on the module proxy")

func (c *catalog) get(ctx context.Context, suffix string) ([]byte, error) {
	// The module path is escaped as the proxy protocol requires: an uppercase
	// letter becomes !x. core's path has none, but building the URL by hand is
	// how that stops being true silently.
	escaped, err := module.EscapePath(corePath)
	if err != nil {
		return nil, err
	}
	endpoint, err := c.base.Parse(escaped + "/" + suffix)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "codefly-core-pin-drift")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return nil, errNotFound
	default:
		return nil, fmt.Errorf("%s returned HTTP %d", endpoint.Redacted(), response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, responseLimit))
}

// latest is the newest stable version core has published.
func (c *catalog) latest() string {
	return c.stable[len(c.stable)-1]
}

// resolves reports whether a version is one the proxy lists. Pseudo-versions
// are never listed, so they are asked about directly.
func (c *catalog) resolves(ctx context.Context, version string) (bool, error) {
	if c.published[version] {
		return true, nil
	}
	if !module.IsPseudoVersion(version) {
		return false, nil
	}
	if _, err := c.publishedAt(ctx, version); err != nil {
		if errors.Is(err, errNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// newerThan returns the stable versions above a pin, ascending. The first is
// the oldest version the pin is missing, which is what the clock reads.
func (c *catalog) newerThan(version string) []string {
	var newer []string
	for _, candidate := range c.stable {
		if semver.Compare(candidate, version) > 0 {
			newer = append(newer, candidate)
		}
	}
	return newer
}

// oldestDescribable returns the oldest version in newer whose publication time
// the proxy will state, and that time.
//
// Normally that is newer[0] and this is one request. It walks on because the
// alternative is worse than imprecise: a version the proxy lists but cannot
// describe would make this gate exit "could not answer" in every repository at
// once, which holds every merge in the fleet on a condition no consumer can
// fix. Reporting the clock from the next version instead understates the drift
// slightly and keeps the gate answering.
func (c *catalog) oldestDescribable(ctx context.Context, newer []string) (string, time.Time, error) {
	var firstErr error
	for _, candidate := range newer {
		when, err := c.publishedAt(ctx, candidate)
		if err == nil {
			return candidate, when, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if !errors.Is(err, errNotFound) {
			return "", time.Time{}, err
		}
	}
	if firstErr == nil {
		firstErr = errNotFound
	}
	return "", time.Time{}, fmt.Errorf(
		"the proxy lists %d version(s) newer than this pin but can describe none of "+
			"them: %w", len(newer), firstErr)
}

// publishedAt returns when a version appeared.
//
// For a pseudo-version the answer is encoded in the version string itself, so
// no request is made — but the proxy is still asked by resolves(), because core
// squash-merges and the commit a pseudo-version names can stop being reachable
// from main, at which point the pin resolves for nobody.
func (c *catalog) publishedAt(ctx context.Context, version string) (time.Time, error) {
	if cached, ok := c.times[version]; ok {
		return cached, nil
	}
	body, err := c.get(ctx, "@v/"+version+".info")
	if err != nil {
		return time.Time{}, err
	}
	var info struct {
		Version string    `json:"Version"`
		Time    time.Time `json:"Time"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return time.Time{}, fmt.Errorf("decode %s info: %w", version, err)
	}
	if info.Time.IsZero() {
		return time.Time{}, fmt.Errorf("the proxy reported no publication time for %s", version)
	}
	c.times[version] = info.Time.UTC()
	return info.Time.UTC(), nil
}
