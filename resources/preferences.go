package resources

// preferences.go — PER-DEVELOPER, PROJECT-SCOPED settings stored at the
// workspace root in `preferences.codefly.yaml` — NOT git-tracked.
//
// ARCHITECTURE: service.codefly.yaml says WHAT a service is (its agent,
// endpoints, dependencies) — that's shared, committed project config. These
// preferences say HOW this developer wants to RUN this project's services on
// THIS machine — e.g. run Go services natively (fast local dev, no nix/docker
// overhead) while running postgres under nix (Docker-free). It is neither
// committed project config nor a global setting: (1) the project shouldn't
// dictate your local runtime, (2) your laptop choice shouldn't be committed,
// (3) different projects on one machine may want different setups — so it lives
// in the PROJECT FOLDER, gitignored. Runtime context is resolved per service,
// falling back to the global --runtime-context / CODEFLY__RUNTIME_CONTEXT.

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UserPreferencesFile is the filename at the workspace root (gitignored).
const UserPreferencesFile = "preferences.codefly.yaml"

// UserPreferences are this developer's machine-local choices. Absent file = an
// empty, all-defaults preference set (never an error).
type UserPreferences struct {
	// Runtime controls per-service runtime backend selection (native/nix/container).
	Runtime *RuntimePreferences `yaml:"runtime,omitempty"`
}

// RuntimePreferences picks a runtime context per service, most specific wins:
// by-service > by-agent > default > (caller's global fallback).
type RuntimePreferences struct {
	// Default applies when nothing more specific matches. Empty = use the
	// caller's global fallback (--runtime-context / CODEFLY__RUNTIME_CONTEXT).
	Default string `yaml:"default,omitempty"`
	// ByAgent maps an agent name (e.g. "postgres", "go-grpc") → runtime context.
	// This is the natural rule: "all my Go services native, postgres nix".
	ByAgent map[string]string `yaml:"by-agent,omitempty"`
	// ByService maps a specific service name → runtime context (highest priority,
	// for one-off overrides).
	ByService map[string]string `yaml:"by-service,omitempty"`
}

// RuntimeContextFor resolves the desired runtime context for a service:
// by-service > by-agent > default > fallback. fallback is the global runtime
// context the run was launched with. nil-safe.
func (p *UserPreferences) RuntimeContextFor(serviceName, agentName, fallback string) string {
	if p == nil || p.Runtime == nil {
		return fallback
	}
	r := p.Runtime
	if v, ok := r.ByService[serviceName]; ok && v != "" {
		return v
	}
	if v, ok := r.ByAgent[agentName]; ok && v != "" {
		return v
	}
	if r.Default != "" {
		return r.Default
	}
	return fallback
}

// LoadUserPreferences reads <workspaceDir>/preferences.codefly.yaml (the
// gitignored, project-scoped, per-developer file). A MISSING file is not an
// error — it returns empty preferences so every service falls back to the
// global runtime context. Only a malformed file errors.
func LoadUserPreferences(workspaceDir string) (*UserPreferences, error) {
	p := filepath.Join(workspaceDir, UserPreferencesFile)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &UserPreferences{}, nil
	}
	if err != nil {
		return nil, err
	}
	var prefs UserPreferences
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &prefs, nil
}
