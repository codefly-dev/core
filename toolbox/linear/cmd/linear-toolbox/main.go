// Command linear-toolbox is the standalone binary form of the codefly linear
// toolbox. Loaded by core/agents/manager.Load like every other agent.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION — version string surfaced in Identity (default "0.0.0-dev").
//	LINEAR_API_KEY          — Linear personal API key (required for any call).
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/linear"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	agents.ServeToolbox(linear.New(os.Getenv("LINEAR_API_KEY"), version))
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
