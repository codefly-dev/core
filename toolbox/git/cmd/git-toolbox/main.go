// Command git-toolbox is the standalone binary form of the codefly
// git toolbox. The host (CLI / Mind) loads it via core/agents/manager.Load
// — the same loader that handles every other agent in the system.
// Toolbox plugins are agents; they just register a Toolbox server
// instead of (or alongside) Runtime/Builder/Tooling.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION   — version string surfaced in Identity.
//	                            Defaults to "0.0.0-dev".
//	CODEFLY_TOOLBOX_WORKSPACE — repository path the toolbox operates
//	                            on. Defaults to the current working
//	                            directory; if neither is set the
//	                            plugin still boots but every git
//	                            operation will fail loudly.
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/git"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	workspace := os.Getenv("CODEFLY_TOOLBOX_WORKSPACE")
	if workspace == "" {
		// Fall back to cwd. The toolbox itself surfaces "open repo"
		// errors if cwd isn't a git tree, which is the right place
		// for that diagnostic.
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	agents.Serve(agents.PluginRegistration{
		Toolbox: git.New(workspace, version),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
