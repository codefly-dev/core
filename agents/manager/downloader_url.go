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
	return downloadURLForPlatform(agent, runtime.GOOS, runtime.GOARCH)
}

// downloadURLForPlatform builds the release asset URL for an explicit platform.
// The platform is a parameter (not read from runtime here) so the asset-name
// contract every publisher repository must satisfy — including linux/arm64,
// which no CI host builds on — is verifiable independent of the running host.
func downloadURLForPlatform(agent *resources.Agent, goos, goarch string) (string, error) {
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
		registration.GitHubAsset(agent.Name, agent.Version, goos, goarch),
	), nil
}
