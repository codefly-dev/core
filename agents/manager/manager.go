package manager

import (
	"context"
	"strings"

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
	tag := release.GetTagName()
	agent.Version = strings.ReplaceAll(tag, "v", "")
	return nil
}
