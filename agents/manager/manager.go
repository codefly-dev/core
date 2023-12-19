package manager

import (
	"context"
	"strings"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/google/go-github/v37/github"
)

func PinToLatestRelease(agent *configurations.Agent) error {
	logger := shared.NewLogger().With("agents.PinToLatestRelease<%s>", agent.Unique())
	client := github.NewClient(nil)
	source := toGithubSource(agent)
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), source.Owner, source.Repo)
	if err != nil {
		return logger.Wrapf(err, "cannot get latest release")
	}
	tag := release.GetTagName()
	agent.Version = strings.Replace(tag, "v", "", -1)
	return nil
}
