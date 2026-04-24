package manager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/google/go-github/v37/github"
)

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
	w := wool.Get(ctx).In("agents.FindLocalLatest", wool.Field("agent", agent.Identifier()))

	base := resources.AgentBase(ctx)
	var subdir string
	if agent.IsService() {
		subdir = "services"
	} else if agent.IsApplication() {
		subdir = "applications"
	} else {
		subdir = "modules"
	}

	dir := filepath.Join(base, "agents", subdir, agent.Publisher)

	if err := findLocalLatestInDir(dir, agent); err != nil {
		return w.Wrapf(err, "finding local latest")
	}

	w.Debug("resolved to local version", wool.Field("version", agent.Version))
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

// ResolveLatest resolves agent.Version when it is "latest". Strategy:
//
//  1. If CODEFLY_AGENT_SOURCE=local: scan the local agent dir only.
//  2. Otherwise: try FindLocalLatest first; if it succeeds, use it.
//     This makes locally-built agents (via `codefly agent build`)
//     take precedence over any GitHub release, which is the intent
//     of running `codefly` from a dev checkout.
//  3. Fall back to PinToLatestRelease (GitHub → local fallback).
//
// No-op when agent.Version is already a concrete semver.
func ResolveLatest(ctx context.Context, agent *resources.Agent) error {
	if agent.Version != "latest" {
		return nil
	}
	w := wool.Get(ctx).In("agents.ResolveLatest", wool.Field("agent", agent.Identifier()))
	if AgentSourceLocal() {
		w.Debug("CODEFLY_AGENT_SOURCE=local — resolving from local agent dir")
		return FindLocalLatest(ctx, agent)
	}
	if err := FindLocalLatest(ctx, agent); err == nil {
		w.Debug("resolved latest from local build", wool.Field("version", agent.Version))
		return nil
	}
	w.Debug("no local build; falling back to GitHub releases")
	return PinToLatestRelease(ctx, agent)
}

// PinToLatestRelease queries GitHub for the latest release tag and updates
// the agent's version. Falls back to FindLocalLatest if GitHub is unreachable
// or has no releases for this agent.
//
// When CODEFLY_AGENT_SOURCE=local (or --local-agents on the CLI),
// GitHub is skipped entirely and resolution goes straight to the local
// filesystem scan. This makes "version: latest" work offline and lets
// agent developers iterate on locally-built binaries without needing
// to cut a GitHub release.
func PinToLatestRelease(ctx context.Context, agent *resources.Agent) error {
	w := wool.Get(ctx).In("agents.PinToLatestRelease", wool.Field("agent", agent.Identifier()))
	if AgentSourceLocal() {
		w.Debug("CODEFLY_AGENT_SOURCE=local — resolving from local agent dir")
		return FindLocalLatest(ctx, agent)
	}
	client := github.NewClient(nil)
	source := toGithubSource(agent)
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), source.Owner, source.Repo)
	if err != nil {
		w.Debug("GitHub release lookup failed, trying local", wool.Field("error", err.Error()))
		return FindLocalLatest(ctx, agent)
	}
	latestVersion := strings.ReplaceAll(release.GetTagName(), "v", "")
	if agent.Version == "latest" {
		agent.Version = latestVersion
		return nil
	}
	currentVersion, err := semver.Make(agent.Version)
	if err != nil {
		return w.Wrapf(err, "invalid current version format")
	}
	newVersion, err := semver.Make(latestVersion)
	if err != nil {
		return w.Wrapf(err, "invalid latest version format")
	}
	if newVersion.GT(currentVersion) {
		agent.Version = latestVersion
	}
	return nil
}
