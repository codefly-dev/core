// Command github-toolbox is the standalone binary form of the codefly github
// toolbox. Loaded by core/agents/manager.Load like every other agent.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION   — version string surfaced in Identity (default "0.0.0-dev").
//	CODEFLY_TOOLBOX_WORKSPACE — repo checkout whose origin remote names owner/repo (default cwd).
//	GITHUB_TOKEN              — GitHub API token used for all calls.
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/github"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	workspace := os.Getenv("CODEFLY_TOOLBOX_WORKSPACE")
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	agents.Serve(agents.PluginRegistration{
		Toolbox: github.New(workspace, os.Getenv("GITHUB_TOKEN"), version),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
