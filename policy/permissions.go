package policy

import (
	"fmt"

	"github.com/codefly-dev/core/runners/sandbox"
)

// NetworkPolicy mirrors sandbox.NetworkPolicy but is the YAML-facing
// type. Translation is one-way at policy.Apply time.
type NetworkPolicy string

const (
	// NetworkDeny severs all network access. Default for new
	// manifests; explicit zero value avoids "what does empty mean?"
	// ambiguity.
	NetworkDeny NetworkPolicy = "deny"

	// NetworkOpen leaves network unrestricted. Explicit opt-in only.
	// Auditors should grep for `network: open` in manifests.
	NetworkOpen NetworkPolicy = "open"
)

// SandboxPolicy is the YAML-shaped permission block a plugin manifest
// declares. Example:
//
//	sandbox:
//	  read_paths:
//	    - "${WORKSPACE}"
//	  write_paths:
//	    - "${WORKSPACE}"
//	    - "${TMPDIR}"
//	  network: deny
//	  unix_sockets:
//	    - "/var/run/docker.sock"  # if this plugin needs docker access
//
// Path strings may use ${WORKSPACE}, ${TMPDIR}, ${HOME} placeholders
// that are expanded at Apply time. Absolute paths pass through.
type SandboxPolicy struct {
	ReadPaths   []string      `yaml:"read_paths,omitempty" json:"read_paths,omitempty"`
	WritePaths  []string      `yaml:"write_paths,omitempty" json:"write_paths,omitempty"`
	Network     NetworkPolicy `yaml:"network,omitempty" json:"network,omitempty"`
	UnixSockets []string      `yaml:"unix_sockets,omitempty" json:"unix_sockets,omitempty"`
}

// Validate checks the policy is internally consistent. Empty policies
// are allowed (zero-trust default applied at Apply time).
func (p *SandboxPolicy) Validate() error {
	for _, r := range p.ReadPaths {
		if r == "" {
			return fmt.Errorf("sandbox.read_paths contains an empty entry")
		}
	}
	for _, w := range p.WritePaths {
		if w == "" {
			return fmt.Errorf("sandbox.write_paths contains an empty entry")
		}
	}
	for _, s := range p.UnixSockets {
		if s == "" {
			return fmt.Errorf("sandbox.unix_sockets contains an empty entry")
		}
	}
	switch p.Network {
	case "", NetworkDeny, NetworkOpen:
		// "" defaults to NetworkDeny at Apply time.
	default:
		return fmt.Errorf("sandbox.network: %q is not one of {deny, open}", p.Network)
	}
	return nil
}

// PathExpander resolves placeholders in path strings: ${WORKSPACE},
// ${TMPDIR}, ${HOME}. Implementations are caller-provided so the
// policy package doesn't need to know how to find the workspace.
type PathExpander interface {
	Expand(s string) (string, error)
}

// Apply translates a SandboxPolicy into a configured sandbox.Sandbox.
// The resulting sandbox is ready to Wrap() commands that should run
// under the policy.
//
// expand is called on every path entry; it should fail loudly when a
// referenced placeholder is unset rather than silently substituting
// "" (which would create an unintended catch-all subpath rule).
func (p *SandboxPolicy) Apply(sb sandbox.Sandbox, expand PathExpander) error {
	for _, raw := range p.ReadPaths {
		expanded, err := expand.Expand(raw)
		if err != nil {
			return fmt.Errorf("expand read_path %q: %w", raw, err)
		}
		sb.WithReadPaths(expanded)
	}
	for _, raw := range p.WritePaths {
		expanded, err := expand.Expand(raw)
		if err != nil {
			return fmt.Errorf("expand write_path %q: %w", raw, err)
		}
		sb.WithWritePaths(expanded)
	}
	for _, raw := range p.UnixSockets {
		expanded, err := expand.Expand(raw)
		if err != nil {
			return fmt.Errorf("expand unix_socket %q: %w", raw, err)
		}
		sb.WithUnixSockets(expanded)
	}

	// Default to deny when unset. Explicit Open in YAML stays Open.
	switch p.Network {
	case "", NetworkDeny:
		sb.WithNetwork(sandbox.NetworkDeny)
	case NetworkOpen:
		sb.WithNetwork(sandbox.NetworkOpen)
	}
	return nil
}
