package manager

import (
	"fmt"
	"runtime"

	"github.com/codefly-dev/core/resources"
)

// DownloadURL returns the platform release asset for the agent's publisher.
// Keeping platform selection in one implementation avoids unsupported build-tag
// gaps (notably linux/arm64) and keeps version lookup and asset download pointed
// at the same publisher repository.
func DownloadURL(agent *resources.Agent) (string, error) {
	source, err := toGithubSource(agent)
	if err != nil {
		return "", err
	}
	registration, err := resources.AgentKindRegistrationFor(agent.Kind)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/v%s/%s",
		source.Owner,
		source.Repo,
		agent.Version,
		registration.GitHubAsset(agent.Name, agent.Version, runtime.GOOS, runtime.GOARCH),
	), nil
}
