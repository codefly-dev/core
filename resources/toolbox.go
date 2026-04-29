package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/wool"
	"gopkg.in/yaml.v3"
)

// ToolboxConfigurationName is the manifest filename a toolbox
// plugin's directory must contain. Mirrors service.codefly.yaml /
// module.codefly.yaml / agent.codefly.yaml.
const ToolboxConfigurationName = "toolbox.codefly.yaml"

// ToolboxAgent is the AgentKind tag for toolbox plugins.
//
// Note: not yet registered in the basev0.Agent_Kind proto enum —
// that requires regenerating proto bindings, which is Phase 1.
// Until then ToolboxAgent is loadable from YAML and usable in Go
// but does NOT round-trip to proto.AgentKind. Code paths that need
// proto serialization should guard with a TODO referencing this
// limitation. Most toolbox tooling is internal and never crosses
// the proto boundary anyway.
const ToolboxAgent = AgentKind("codefly:toolbox")

// Toolbox is a narrow, capability-focused plugin.
//
// Toolboxes (Code, Git, Docker, Nix, Bash, Web, gRPC) are codefly's
// vocabulary for cross-cutting operations that agents need to perform.
// Each one is its own permission boundary: the Git toolbox declares
// `canonical_for: [git]` and from that moment no agent's bash can
// invoke `git` directly — they MUST call the typed RPCs.
//
// Distinct from Service:
//   - Service plugins ship user-deployable processes (the API, the
//     auth-sidecar, the postgres instance, etc.).
//   - Toolboxes are platform utilities. They run as codefly-internal
//     gRPC servers exposing MCP-shape Tool/Resource/Prompt primitives.
//
// Manifest example (Git toolbox):
//
//	# toolbox.codefly.yaml
//	name: git
//	version: 0.0.1
//	description: Git repository operations as typed RPCs.
//	agent:
//	  kind: codefly:toolbox
//	  name: git
//	  publisher: codefly.dev
//	  version: 0.0.1
//	sandbox:
//	  read_paths:  ["${WORKSPACE}"]
//	  write_paths: ["${WORKSPACE}"]
//	  network: deny
//	canonical_for: [git]
type Toolbox struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Version     string `yaml:"version"`

	Agent *Agent `yaml:"agent"`

	// Sandbox declares the OS-level confinement this toolbox's
	// processes run under. Translated to a sandbox.Sandbox at
	// runtime via policy.SandboxPolicy.Apply. Empty/missing block
	// means "deny network, no writes outside cwd" defaults — secure
	// by default.
	Sandbox policy.SandboxPolicy `yaml:"sandbox,omitempty"`

	// CanonicalFor lists binaries this toolbox claims as the canonical
	// owner. Bash toolbox + policy.CanonicalRegistry consult this list
	// at registry-build time; any agent's bash that tries to invoke one
	// of these binaries is refused with a hint pointing here.
	//
	// Conflicts (two plugins claiming the same binary) are load-time
	// errors, surfaced when the registry is built.
	CanonicalFor []string `yaml:"canonical_for,omitempty"`

	// Spec is the toolbox-specific configuration. Mirrors Service.Spec.
	Spec map[string]any `yaml:"spec,omitempty"`

	// internal
	dir string
}

// LoadToolboxFromDir reads a Toolbox manifest from
// `<dir>/toolbox.codefly.yaml`. Returns a useful error when the file
// is missing — toolboxes are expected to ship the manifest at the
// root of their plugin directory.
func LoadToolboxFromDir(ctx context.Context, dir string) (*Toolbox, error) {
	w := wool.Get(ctx).In("LoadToolboxFromDir", wool.DirField(dir))
	path := filepath.Join(dir, ToolboxConfigurationName)
	w.Trace("loading toolbox manifest", wool.Field("path", path))

	data, err := os.ReadFile(path) //nolint:gosec // path is composed from a caller-trusted dir
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tb := &Toolbox{}
	if err := yaml.Unmarshal(data, tb); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	tb.dir = dir
	if err := tb.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return tb, nil
}

// Validate enforces the invariants the rest of the pipeline depends
// on. Surfaced at load time so a malformed toolbox doesn't fail
// halfway through registration.
func (t *Toolbox) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("toolbox.name is required")
	}
	if t.Version == "" {
		return fmt.Errorf("toolbox.version is required")
	}
	if t.Agent == nil {
		return fmt.Errorf("toolbox.agent is required")
	}
	if t.Agent.Kind != ToolboxAgent {
		return fmt.Errorf("toolbox.agent.kind must be %q (got %q)", ToolboxAgent, t.Agent.Kind)
	}
	if err := t.Sandbox.Validate(); err != nil {
		return fmt.Errorf("toolbox.sandbox: %w", err)
	}
	for i, b := range t.CanonicalFor {
		if b == "" {
			return fmt.Errorf("toolbox.canonical_for[%d] is empty", i)
		}
	}
	return nil
}

// Dir returns the directory the toolbox manifest was loaded from.
// Returns "" if the toolbox was constructed in-memory (e.g. tests).
func (t *Toolbox) Dir() string { return t.dir }

// Identity is the (name, version) pair used for log lines and
// registry diagnostics. Format mirrors Service.Identity for grep
// uniformity.
func (t *Toolbox) Identity() string {
	return fmt.Sprintf("%s@%s", t.Name, t.Version)
}

// RegisterCanonical contributes this toolbox's CanonicalFor binaries
// to the given registry, with this toolbox's Name as the owner.
//
// Returns the same error as policy.CanonicalRegistry.Claim — most
// notably, a load-time conflict when two plugins both claim the same
// binary. That conflict surfaces here, BEFORE any agent invocation,
// which is exactly when an operator can fix it (drop one of the
// plugins, or rename a manifest).
//
// No-op when CanonicalFor is empty.
func (t *Toolbox) RegisterCanonical(reg *policy.CanonicalRegistry) error {
	if len(t.CanonicalFor) == 0 {
		return nil
	}
	return reg.Claim(t.Name, t.CanonicalFor...)
}

// BuildCanonicalRegistry composes a registry from a set of loaded
// toolbox manifests. The conventional entry point at workspace-load
// time: load every plugin under agents/toolboxes/ and pass them in;
// the result is a frozen registry the runtime consults on every bash
// invocation.
//
// The order plugins are passed in matters for collision diagnostics
// only — the second-claimer's error names the first as the existing
// owner, so reproducible loading produces reproducible error messages.
func BuildCanonicalRegistry(toolboxes ...*Toolbox) (*policy.CanonicalRegistry, error) {
	reg := policy.NewCanonicalRegistry()
	for _, t := range toolboxes {
		if err := t.RegisterCanonical(reg); err != nil {
			return nil, fmt.Errorf("toolbox %q: %w", t.Identity(), err)
		}
	}
	return reg, nil
}
