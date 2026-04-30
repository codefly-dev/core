// Command nix-toolbox is the standalone binary form of the codefly
// nix toolbox. Loaded via the standard agent loader (core/agents/manager.Load);
// registers a Toolbox server through agents.Serve.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION   — Identity version. Default "0.0.0-dev".
//	CODEFLY_TOOLBOX_NIX_BIN   — override the nix binary path. Mostly
//	                             useful for tests; production callers
//	                             leave it unset and rely on PATH.
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/nix"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	server := nix.New(version)
	if bin := os.Getenv("CODEFLY_TOOLBOX_NIX_BIN"); bin != "" {
		server = server.WithBinary(bin)
	}
	agents.Serve(agents.PluginRegistration{
		Toolbox: server,
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
