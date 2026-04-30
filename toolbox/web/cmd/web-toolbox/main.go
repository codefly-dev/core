// Command web-toolbox is the standalone binary form of the codefly
// web toolbox. Loaded via the standard agent loader (core/agents/manager.Load);
// registers a Toolbox server through agents.Serve.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION         — Identity version. Default "0.0.0-dev".
//	CODEFLY_TOOLBOX_ALLOWED_DOMAINS — comma-separated allowlist. The
//	                                  toolbox starts with no allowed
//	                                  domains (deny by default) if
//	                                  unset.
package main

import (
	"os"
	"strings"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/web"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")

	server := web.New(version)
	if raw := os.Getenv("CODEFLY_TOOLBOX_ALLOWED_DOMAINS"); raw != "" {
		// Trim each entry — trailing whitespace from poorly-formatted
		// env vars is one of the easier-to-introduce bugs ("example.com ,
		// foo.com" would land "example.com " on the allowlist and never
		// match).
		parts := strings.Split(raw, ",")
		clean := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				clean = append(clean, t)
			}
		}
		server = server.WithAllowedDomains(clean...)
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
