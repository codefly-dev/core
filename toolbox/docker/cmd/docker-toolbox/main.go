// Command docker-toolbox is the standalone binary form of the codefly
// docker toolbox. Loaded via the standard agent loader (core/agents/manager.Load);
// registers a Toolbox server through agents.Serve.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION — Identity version. Default "0.0.0-dev".
//	(The Docker daemon is discovered from the standard DOCKER_HOST/
//	DOCKER_TLS_VERIFY/DOCKER_CERT_PATH env vars via the Docker SDK.)
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/toolbox/docker"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	agents.Serve(agents.PluginRegistration{
		Toolbox: docker.New(version),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
