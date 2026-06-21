// Command codefly-toolbox is the standalone binary form of the codefly
// workspace-inspection toolbox. Loaded by core/agents/manager.Load like every
// other agent.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION   — version surfaced in Identity (default "0.0.0-dev").
//	CODEFLY_TOOLBOX_WORKSPACE — workspace dir to inspect (default cwd).
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/codefly"
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
		Toolbox: codefly.New(workspace, version),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
