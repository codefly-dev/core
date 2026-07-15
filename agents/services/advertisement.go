package services

import (
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	base "github.com/codefly-dev/core/runners/base"
)

// Advertisement declares the parts of an agent's AgentInformation that vary per
// plugin. Build() fills in the parts that are the SAME for every plugin — the
// capabilities (BUILDER+RUNTIME, plus HOT_RELOAD when requested) and the
// host-filtered, preference-ordered SupportedBackends — so every plugin
// advertises uniformly. This is the single place agents construct their
// AgentInformation: `return services.Advertisement{...}.Build(), nil`.
type Advertisement struct {
	// Backends declares which execution backends the plugin is capable of; they
	// are resolved to what is actually installed on this host (LOCAL>NIX>DOCKER).
	Backends base.BackendSupport
	// Toolchains the plugin needs to run in LOCAL mode.
	Toolchains []agentv0.Toolchain_Type
	// HotReload advertises HOT_RELOAD in addition to BUILDER+RUNTIME.
	HotReload bool
	// RuntimeOnly advertises RUNTIME only (no BUILDER) — for passthrough agents.
	RuntimeOnly bool
	// Languages the agent can create, run, or analyze.
	Languages []agentv0.Language_Type
	// Protocols the agent can expose.
	Protocols []agentv0.Protocol_Type
	// ReadMe is the human-facing documentation (usually a rendered template).
	ReadMe string
	// Config documents the service YAML fields the agent understands.
	Config []*agentv0.ConfigurationValueDetail
	// Techniques are reusable workflows/prompts the agent can apply.
	Techniques []*agentv0.AgentTechnique
}

// Build assembles the AgentInformation, applying the common defaults.
func (a Advertisement) Build() *agentv0.AgentInformation {
	var caps []*agentv0.Capability
	if a.RuntimeOnly {
		caps = append(caps, &agentv0.Capability{Type: agentv0.Capability_RUNTIME})
	} else {
		caps = append(caps,
			&agentv0.Capability{Type: agentv0.Capability_BUILDER},
			&agentv0.Capability{Type: agentv0.Capability_RUNTIME},
		)
	}
	if a.HotReload {
		caps = append(caps, &agentv0.Capability{Type: agentv0.Capability_HOT_RELOAD})
	}

	info := &agentv0.AgentInformation{
		SupportedBackends:    a.Backends.ResolveBackends(),
		Capabilities:         caps,
		ReadMe:               a.ReadMe,
		ConfigurationDetails: a.Config,
		Techniques:           a.Techniques,
		Protocols:            []*agentv0.Protocol{},
	}
	for _, t := range a.Toolchains {
		info.Toolchains = append(info.Toolchains, &agentv0.Toolchain{Type: t})
	}
	for _, l := range a.Languages {
		info.Languages = append(info.Languages, &agentv0.Language{Type: l})
	}
	for _, p := range a.Protocols {
		info.Protocols = append(info.Protocols, &agentv0.Protocol{Type: p})
	}
	return info
}
