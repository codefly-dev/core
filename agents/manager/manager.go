package manager

import (
	"context"
	"strings"

	"github.com/blang/semver"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/google/go-github/v37/github"
)

func PinToLatestRelease(ctx context.Context, agent *resources.Agent) error {
	w := wool.Get(ctx).In("agents.PinToLatestRelease", wool.Field("agent", agent.Identifier()))
	client := github.NewClient(nil)
	source := toGithubSource(agent)
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), source.Owner, source.Repo)
	if err != nil {
		return w.Wrapf(err, "cannot get latest release")
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
