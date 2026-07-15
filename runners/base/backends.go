package base

import (
	"os/exec"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// BackendSupport declares which execution backends a plugin is CAPABLE of, so
// ResolveBackends can filter them down to what is actually available on THIS
// host. Every plugin builds its advertised SupportedBackends through this, which
// keeps the preference order uniform (LOCAL > NIX > DOCKER) and makes the list
// dynamic: e.g. a plugin drops NIX when nix is not installed on the machine.
type BackendSupport struct {
	// Local, when non-nil, is the plugin's OWN check for whether it can run
	// natively on this host (its toolchain is installed). nil means the plugin
	// has no LOCAL path (e.g. a database that only runs as a container / nix).
	Local func() bool
	// Nix is true when the plugin has a nix path; kept only if nix is installed
	// and supported on this OS.
	Nix bool
	// Docker is true when the plugin ships a container image; kept only if the
	// docker CLI is installed. (Daemon reachability is a run-time concern the
	// CLI resolves separately.)
	Docker bool
}

// ResolveBackends returns the plugin's available execution backends on THIS
// host, in the canonical PREFERENCE ORDER LOCAL > NIX > DOCKER. Call it from an
// agent's GetAgentInformation so the advertised list reflects the real machine:
// LOCAL only when the toolchain is present, NIX only when nix is installed,
// DOCKER only when the docker CLI exists. An EMPTY result means nothing is
// runnable here — the CLI turns that into an actionable "no runtime backend
// available" error rather than failing deep in startup.
func (s BackendSupport) ResolveBackends() []*agentv0.Backend {
	return s.resolveBackends(CheckNixInstalled, IsNixSupported, DockerInstalled)
}

func (s BackendSupport) resolveBackends(
	nixInstalled func() bool,
	nixSupported func() bool,
	dockerInstalled func() bool,
) []*agentv0.Backend {
	var out []*agentv0.Backend
	if s.Local != nil && s.Local() {
		out = append(out, &agentv0.Backend{Type: agentv0.Backend_LOCAL})
	}
	if s.Nix && nixInstalled() && nixSupported() {
		out = append(out, &agentv0.Backend{Type: agentv0.Backend_NIX})
	}
	if s.Docker && dockerInstalled() {
		out = append(out, &agentv0.Backend{Type: agentv0.Backend_DOCKER})
	}
	return out
}

// DockerInstalled reports whether the docker CLI is on PATH. Whether the daemon
// is actually reachable is resolved later by the CLI (flow.resolveDockerFallback).
func DockerInstalled() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}
