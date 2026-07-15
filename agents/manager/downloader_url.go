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
func DownloadURL(agent *resources.Agent) string {
	source := toGithubSource(agent)
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/v%s/service-%s_%s_%s_%s.tar.gz",
		source.Owner,
		source.Repo,
		agent.Version,
		agent.Name,
		agent.Version,
		runtime.GOOS,
		runtime.GOARCH,
	)
}
