package manager

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/google/go-github/v89/github"
)

// newGitHubReleaseClient returns a go-github client authenticated with
// GITHUB_TOKEN/GH_TOKEN when either is set, matching the token-via-transport
// convention used elsewhere in core (toolbox/github). Unauthenticated GitHub
// API access is capped at 60 requests/hour per IP, so resolving "latest" for
// several agents can flakily 403; a token raises the cap to 5000/hour.
func newGitHubReleaseClient() *github.Client {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	var options []github.ClientOptionsFunc
	if token == "" {
		client, err := github.NewClient()
		if err != nil {
			panic(fmt.Sprintf("configure GitHub release client: %v", err))
		}
		return client
	}
	options = append(options, github.WithHTTPClient(&http.Client{Transport: githubTokenTransport{token: token}}))
	client, err := github.NewClient(options...)
	if err != nil {
		panic(fmt.Sprintf("configure authenticated GitHub release client: %v", err))
	}
	return client
}

// latestReleaseTag returns the newest published release tag for a repository.
// It is a variable so resolution can be exercised without network access.
var latestReleaseTag = githubLatestReleaseTag

func githubLatestReleaseTag(ctx context.Context, owner, repo string) (string, error) {
	release, _, err := newGitHubReleaseClient().Repositories.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return release.GetTagName(), nil
}

// githubTokenTransport adds a bearer token to each request without pulling in
// the oauth2 dependency (same approach as core/toolbox/github).
type githubTokenTransport struct{ token string }

func (t githubTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

// AgentSourceEnv selects where "latest" agent versions are resolved from.
//   - "" or "remote" (default): GitHub releases first, fall back to local.
//   - "local":                  scan ~/.codefly/agents/ only, never call GitHub.
//
// Set via the CLI's `--local-agents` persistent flag or by exporting
// CODEFLY_AGENT_SOURCE=local. Useful for offline work and for agent
// development where the local build is the source of truth.
const AgentSourceEnv = "CODEFLY_AGENT_SOURCE"

// AgentSourceLocal returns true when the agent loader should bypass
// GitHub and resolve versions exclusively from the local agent
// directory. See AgentSourceEnv.
func AgentSourceLocal() bool {
	return strings.EqualFold(os.Getenv(AgentSourceEnv), "local")
}

// FindLocalLatest scans the local agent directory for installed binaries
// matching the agent name and returns the highest semver version found.
// This is the preferred resolution path for locally-built agents (via
// "codefly agent build") that have no GitHub release.
func FindLocalLatest(ctx context.Context, agent *resources.Agent) error {
	if agent == nil {
		return fmt.Errorf("agent is required")
	}
	if _, err := agent.Proto(); err != nil {
		return err
	}
	w := wool.Get(ctx).In("agents.FindLocalLatest", wool.Field("agent", agent.Identifier()))

	base := resources.AgentBase(ctx)
	registration, err := resources.AgentKindRegistrationFor(agent.Kind)
	if err != nil {
		return w.Wrap(err)
	}
	if !registration.Operations.Version || registration.Resolution.Local == resources.AgentResolutionDisabled {
		return w.NewError("agent kind %s does not support local version resolution", agent.Kind)
	}

	dir := filepath.Join(base, "agents", registration.InstallSubdirectory, agent.Publisher)

	if err := findLocalLatestInDir(dir, agent); err != nil {
		return w.Wrapf(err, "finding local latest")
	}

	w.Trace("resolved to local version", wool.Field("version", agent.Version))
	return nil
}

// findLocalLatestInDir scans dir for files matching "<agent.Name>__<semver>"
// and sets agent.Version to the highest version found.
func findLocalLatestInDir(dir string, agent *resources.Agent) error {
	prefix := agent.Name + "__"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("scan agent directory %s: %w", dir, err)
	}

	var best semver.Version
	found := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		verStr := strings.TrimPrefix(name, prefix)
		v, err := semver.Make(verStr)
		if err != nil {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("inspect local agent %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		if !found || v.GT(best) {
			best = v
			found = true
		}
	}

	if !found {
		return fmt.Errorf("no local binaries for agent %s/%s in %s", agent.Publisher, agent.Name, dir)
	}

	agent.Version = best.String()
	return nil
}

// VersionLatest is the version token that selects the newest published release
// rather than pinning one.
const VersionLatest = "latest"

// SelectsLatest reports whether a version selects the newest published release
// rather than pinning one. An omitted version is that selection's other
// spelling, so it resolves exactly as the literal "latest" does.
func SelectsLatest(version string) bool {
	return version == "" || version == VersionLatest
}

// ResolveLatest resolves agent.Version when it selects the latest release and
// reports where the version came from, so the caller can render a single
// aggregated resolution line instead of the per-step cascade (which is now
// TRACE). Sources:
//   - "pinned": agent.Version was already a concrete semver (no resolution done).
//   - "local":  resolved from a locally-built binary in the agent dir.
//   - "github": resolved from a GitHub release.
//
// Strategy:
//
//  1. If CODEFLY_AGENT_SOURCE=local: scan the local agent dir only.
//  2. Otherwise: PinToLatestRelease (GitHub, falling back to the local dir only
//     when GitHub cannot be reached).
//
// An installed build never outranks a published release here. Preferring one
// would leave a machine that installed an agent last week running it after a
// newer release ships, while the manifest still reads "latest" —
// CODEFLY_AGENT_SOURCE=local (--local-agents) is how a dev checkout asks for
// its own builds.
func ResolveLatest(ctx context.Context, agent *resources.Agent) (string, error) {
	if !SelectsLatest(agent.Version) {
		return "pinned", nil
	}
	w := wool.Get(ctx).In("agents.ResolveLatest", wool.Field("agent", agent.Identifier()))
	if AgentSourceLocal() {
		w.Trace("CODEFLY_AGENT_SOURCE=local — resolving from local agent dir")
		return "local", FindLocalLatest(ctx, agent)
	}
	if !resolvesFromGitHubRelease(agent) {
		w.Trace("agent kind publishes no releases — resolving from local agent dir")
		return "local", FindLocalLatest(ctx, agent)
	}
	return PinToLatestRelease(ctx, agent)
}

// resolvesFromGitHubRelease reports whether this agent kind publishes GitHub
// releases. A kind that does not has no published release to prefer, so its
// local builds stay the only resolution available.
func resolvesFromGitHubRelease(agent *resources.Agent) bool {
	registration, err := resources.AgentKindRegistrationFor(agent.Kind)
	return err == nil && registration.Resolution.GitHub == resources.AgentResolutionGitHubRelease
}

// PinToLatestRelease queries GitHub for the latest release tag and updates
// the agent's version. Falls back to FindLocalLatest if GitHub is unreachable
// or has no releases for this agent. It returns the source the version was
// actually resolved from — "github" for a release lookup, "local" when the
// local-filesystem fallback (or CODEFLY_AGENT_SOURCE=local) supplied it — so
// callers can report the true origin rather than assuming GitHub.
//
// When CODEFLY_AGENT_SOURCE=local (or --local-agents on the CLI),
// GitHub is skipped entirely and resolution goes straight to the local
// filesystem scan. This makes "version: latest" work offline and lets
// agent developers iterate on locally-built binaries without needing
// to cut a GitHub release.
func PinToLatestRelease(ctx context.Context, agent *resources.Agent) (string, error) {
	w := wool.Get(ctx).In("agents.PinToLatestRelease", wool.Field("agent", agent.Identifier()))
	// An omitted version is the latest selector's other spelling. Canonicalize
	// it once here so nothing downstream — the proto validation behind
	// toGithubSource included — has to know both.
	if SelectsLatest(agent.Version) {
		agent.Version = VersionLatest
	}
	if AgentSourceLocal() {
		w.Debug("CODEFLY_AGENT_SOURCE=local — resolving from local agent dir")
		return "local", FindLocalLatest(ctx, agent)
	}
	registration, err := resources.AgentKindRegistrationFor(agent.Kind)
	if err != nil {
		return "", w.Wrap(err)
	}
	if registration.Resolution.GitHub != resources.AgentResolutionGitHubRelease {
		return "", w.NewError("agent kind %s does not support GitHub release resolution", agent.Kind)
	}
	source, err := toGithubSource(agent)
	if err != nil {
		return "", w.Wrap(err)
	}
	tag, err := latestReleaseTag(ctx, source.Owner, source.Repo)
	if err != nil {
		w.Debug("GitHub release lookup failed, trying local", wool.Field("error", err.Error()))
		return "local", FindLocalLatest(ctx, agent)
	}
	// TrimPrefix, not ReplaceAll: ReplaceAll("v","") stripped EVERY 'v' in the
	// tag (e.g. v0.0.1-vault → 0.0.1-ault), corrupting the resolved version.
	latestVersion := strings.TrimPrefix(tag, "v")
	if agent.Version == VersionLatest {
		agent.Version = latestVersion
		return "github", nil
	}
	currentVersion, err := semver.Make(agent.Version)
	if err != nil {
		return "", w.Wrapf(err, "invalid current version format")
	}
	newVersion, err := semver.Make(latestVersion)
	if err != nil {
		return "", w.Wrapf(err, "invalid latest version format")
	}
	if newVersion.GT(currentVersion) {
		agent.Version = latestVersion
	}
	return "github", nil
}
