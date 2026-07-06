package dockerrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
)

const defaultDockerHost = "unix:///var/run/docker.sock"

// dockerHost is a resolved Docker daemon endpoint and where it came from.
// The source is surfaced in "daemon not reachable" errors so a misconfigured
// context vs a stale DOCKER_HOST is immediately distinguishable.
type dockerHost struct {
	Host   string
	Source string
}

// resolveDockerHost mirrors how the `docker` CLI picks its endpoint, which the
// Docker Go SDK's client.FromEnv does NOT: FromEnv honors DOCKER_HOST and
// otherwise hardcodes the default socket, ignoring the active `docker context`.
// On OrbStack/colima/Docker-Desktop the daemon is reachable only via the
// context endpoint, so FromEnv-based detection reports Docker as down.
//
// Resolution order, matching the CLI:
//  1. DOCKER_HOST — explicit override.
//  2. active docker context (DOCKER_CONTEXT env, else currentContext in
//     ~/.docker/config.json) → its docker endpoint from
//     ~/.docker/contexts/meta/<id>/meta.json.
//  3. the default unix socket.
func resolveDockerHost() dockerHost {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return dockerHost{Host: h, Source: "DOCKER_HOST env"}
	}
	if name := activeDockerContextName(); name != "" && name != "default" {
		if h, err := dockerContextHost(name); err == nil && h != "" {
			return dockerHost{Host: h, Source: fmt.Sprintf("docker context %q", name)}
		}
	}
	return dockerHost{Host: defaultDockerHost, Source: "default socket"}
}

// newDockerClient builds a Docker client pointed at the resolved endpoint.
// FromEnv is kept first so TLS material (DOCKER_TLS_VERIFY / DOCKER_CERT_PATH)
// is still honored; WithHost then overrides the endpoint with the resolved one.
func newDockerClient() (*client.Client, dockerHost, error) {
	host := resolveDockerHost()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(host.Host), client.WithAPIVersionNegotiation())
	return cli, host, err
}

// NewClient builds a Docker client that honors the active docker context,
// mirroring the docker CLI's endpoint resolution (see resolveDockerHost). It
// exists so packages outside dockerrun — notably the image builder — reach the
// same engine as the runners instead of client.FromEnv's context-unaware default.
func NewClient() (*client.Client, error) {
	cli, _, err := newDockerClient()
	return cli, err
}

func activeDockerContextName() string {
	if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(dockerConfigDir(), "config.json"))
	if err != nil {
		return ""
	}
	var parsed struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return parsed.CurrentContext
}

// dockerContextHost reads the docker endpoint for a named context. The on-disk
// directory is keyed by the hex SHA-256 of the context name, matching the
// docker CLI's context store layout.
func dockerContextHost(name string) (string, error) {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	data, err := os.ReadFile(filepath.Join(dockerConfigDir(), "contexts", "meta", id, "meta.json"))
	if err != nil {
		return "", err
	}
	var parsed struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	return parsed.Endpoints.Docker.Host, nil
}

func dockerConfigDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}
