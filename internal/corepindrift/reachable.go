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
	"strings"
)

// defaultBranch is the branch a pinned commit must be reachable from.
//
// Hardcoded to the same literal .github/workflows/version-tag.yml declares,
// and for the same reason: it is a fact about this repository, not a value to
// resolve. Resolving it would cost a request whose answer decides whether the
// gate can run at all.
const defaultBranch = "main"

// errReachabilityUnknown reports that reachability could not be established —
// rate limit, network, a response that could not be read. It is deliberately
// distinct from "not reachable": one is a missing answer, the other is a
// refusal, and collapsing them would either wedge the fleet on a rate limit or
// pass a destroyed pin on one.
var errReachabilityUnknown = errors.New("reachability could not be established")

// reachability answers whether a commit is part of a branch's history.
//
// WHY THIS EXISTS, and why the module proxy cannot answer it.
//
// obin-ai/module-robin's main went red for every Go job on 2026-11-04: robin#659
// squash-merged and deleted the branch holding a commit its own go.mod pinned,
// and `go mod download` then failed repository-wide with "unknown revision".
// Robin's pin gate had passed on that commit — it certified a reference that
// merging would destroy.
//
// A pseudo-version naming a commit that is NOT an ancestor of the default
// branch is exactly that pin: it resolves today because the branch still
// exists, and it stops resolving the moment that branch is squash-merged and
// deleted. Core squash-merges, so every such pin in this fleet is one merge
// away from being dead.
//
// proxy.golang.org cannot answer this, which is the part worth being precise
// about: the proxy is a MIRROR and caches a module version permanently. Once it
// has served a pseudo-version it goes on serving it after the commit is gone
// upstream — so a proxy lookup succeeding proves the proxy once saw the commit,
// never that the commit is still there. Any consumer resolving direct (a
// private module, GOPRIVATE, GOFLAGS=-mod=mod against the source) gets the real
// answer and fails. That is why this asks the repository, over the HTTP API,
// rather than inferring it from the proxy.
type reachability struct {
	client *http.Client
	base   *url.URL
	token  string
	owner  string
	repo   string

	// answers caches by sha. A fleet repository pins the same commit from many
	// modules — module-tracing pins one commit from all four — so this is
	// normally one request per distinct commit and not one per module.
	answers map[string]reachAnswer
}

type reachAnswer struct {
	reachable bool
	// aheadBy is how many commits the pinned commit has that the default
	// branch does not. Zero means it is an ancestor; anything else means it
	// sits on a branch, and merging that branch will destroy it.
	aheadBy int
	// missing reports a commit the repository does not have at all.
	missing bool
	err     error
}

// newReachability builds a checker for the repository a module path names.
//
// The token is optional: the API answers for a public repository without one,
// but unauthenticated requests are rate limited per IP and GitHub's hosted
// runners share IPs, so CI passes GITHUB_TOKEN and gets the authenticated
// limit. A read-only token on a public repository is the weakest credential
// available, which is why this gate can hold one and stay outside the model in
// docs/ci-credentials.md.
func newReachability(apiBase string, modulePath string) (*reachability, error) {
	owner, repo, ok := githubRepoOf(modulePath)
	if !ok {
		return nil, fmt.Errorf("%s is not a github.com module path, so its commits "+
			"cannot be checked for reachability", modulePath)
	}
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	base, err := url.Parse(strings.TrimSuffix(apiBase, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("parse API base URL: %w", err)
	}
	return &reachability{
		client:  &http.Client{Timeout: requestTimeout},
		base:    base,
		token:   firstNonEmpty(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")),
		owner:   owner,
		repo:    repo,
		answers: map[string]reachAnswer{},
	}, nil
}

// githubRepoOf splits github.com/<owner>/<repo> out of a module path.
func githubRepoOf(modulePath string) (owner, repo string, ok bool) {
	parts := strings.Split(modulePath, "/")
	if len(parts) < 3 || parts[0] != "github.com" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// reachableFromDefaultBranch reports whether sha is an ancestor of the default
// branch.
//
// It reads `ahead_by` rather than matching on `status`. The status string
// distinguishes "behind" from "diverged" from "ahead", but the question here is
// one thing — does this commit have anything the default branch does not — and
// `ahead_by == 0` is that question directly. Measured against the real API:
// a commit on core's main reports `status: behind, ahead_by: 0`; a commit on an
// unmerged branch reports `status: diverged, ahead_by: 1`.
func (r *reachability) reachableFromDefaultBranch(ctx context.Context, sha string) reachAnswer {
	if cached, ok := r.answers[sha]; ok {
		return cached
	}
	answer := r.ask(ctx, sha)
	r.answers[sha] = answer
	return answer
}

func (r *reachability) ask(ctx context.Context, sha string) reachAnswer {
	endpoint, err := r.base.Parse(fmt.Sprintf("repos/%s/%s/compare/%s...%s",
		url.PathEscape(r.owner), url.PathEscape(r.repo),
		url.PathEscape(defaultBranch), url.PathEscape(sha)))
	if err != nil {
		return reachAnswer{err: fmt.Errorf("%w: %v", errReachabilityUnknown, err)}
	}
	// per_page=1 because the commit list is not read: only the comparison's
	// own counters are.
	query := endpoint.Query()
	query.Set("per_page", "1")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return reachAnswer{err: fmt.Errorf("%w: %v", errReachabilityUnknown, err)}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "codefly-core-pin-drift")
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}

	response, err := r.client.Do(request)
	if err != nil {
		return reachAnswer{err: fmt.Errorf("%w: %v", errReachabilityUnknown, err)}
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// The repository does not have this commit at all. This is the state
		// module-robin was left in after robin#659 deleted the merged branch.
		return reachAnswer{missing: true}
	case http.StatusForbidden, http.StatusTooManyRequests:
		return reachAnswer{err: fmt.Errorf(
			"%w: the GitHub API refused with HTTP %d (rate limit). Set GITHUB_TOKEN "+
				"for the authenticated limit", errReachabilityUnknown, response.StatusCode)}
	default:
		return reachAnswer{err: fmt.Errorf("%w: the GitHub API returned HTTP %d",
			errReachabilityUnknown, response.StatusCode)}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	if err != nil {
		return reachAnswer{err: fmt.Errorf("%w: %v", errReachabilityUnknown, err)}
	}
	var compared struct {
		Status  string `json:"status"`
		AheadBy *int   `json:"ahead_by"`
	}
	if err := json.Unmarshal(body, &compared); err != nil {
		return reachAnswer{err: fmt.Errorf("%w: decode the comparison: %v",
			errReachabilityUnknown, err)}
	}
	if compared.AheadBy == nil {
		// A response with no counter is one this cannot read. Unknown, not
		// reachable: a guard that treats a missing field as a pass is a guard
		// that passes every response shape it does not understand.
		return reachAnswer{err: fmt.Errorf(
			"%w: the comparison carried no ahead_by (status %q)",
			errReachabilityUnknown, compared.Status)}
	}
	return reachAnswer{reachable: *compared.AheadBy == 0, aheadBy: *compared.AheadBy}
}

// unverifiedFor renders the note a finding carries when reachability could not
// be established, so an unanswered question never reads as a clean answer.
func unverifiedFor(err error) string {
	return fmt.Sprintf(
		"reachability from %s was NOT verified (%v), so this pin may name a commit "+
			"that a squash-merge has already destroyed", defaultBranch, err)
}
