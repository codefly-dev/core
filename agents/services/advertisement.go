package services

import (
	"github.com/codefly-dev/core/agents/contract"
	agentv1 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v1"
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
	// BackendsComplete advertises runtime-backend-declaration/v1: Backends is
	// the complete set of ways this plugin can run, so a host may substitute a
	// declared backend for a runtime context the plugin cannot honour, and may
	// read an empty resolution as "nothing here can run this plugin". Leave it
	// false while Backends is only partly declared.
	BackendsComplete bool
	// Toolchains the plugin needs to run in LOCAL mode.
	Toolchains []agentv1.Toolchain_Type
	// HotReload advertises HOT_RELOAD in addition to BUILDER+RUNTIME.
	HotReload bool
	// RuntimeOnly advertises RUNTIME only (no BUILDER) — for passthrough agents.
	RuntimeOnly bool
	// CapabilityOnly suppresses BUILDER/RUNTIME for plugins that expose only an
	// additive capability such as ExecutionExporter.
	CapabilityOnly bool
	// ExecutionExporter advertises the product-neutral receipt exporter service.
	ExecutionExporter bool
	// Languages the agent can create, run, or analyze.
	Languages []agentv1.Language_Type
	// Protocols the agent can expose.
	Protocols []agentv1.Protocol_Type
	// ReadMe is the human-facing documentation (usually a rendered template).
	ReadMe string
	// Config documents the service YAML fields the agent understands.
	Config []*agentv1.ConfigurationValueDetail
	// Techniques are reusable workflows/prompts the agent can apply.
	Techniques []*agentv1.AgentTechnique
	// Validation is the authoritative validation contract. Leave nil only for a
	// legacy agent that has not migrated; an explicit empty message advertises
	// that no validation operations are supported.
	Validation *agentv1.ValidationCapabilities
	// EffectiveInputsVersions advertises implemented discovery schemas.
	EffectiveInputsVersions []uint32
	// UsageContract is the agent's usage.codefly.yaml, verbatim (usually
	// embedded beside agent.codefly.yaml): what the agent tells the code that
	// consumes the services it manages. It is carried unparsed, so the reader
	// refuses a malformed one with the contract's own rules; an agent holds its
	// file to them in its own suite with contracts/usage's Parse. Leave empty
	// for an agent that states no usage rules.
	UsageContract string
}

// Build assembles the AgentInformation, applying the common defaults.
func (a Advertisement) Build() *agentv1.AgentInformation {
	var caps []*agentv1.Capability
	if !a.CapabilityOnly {
		if a.RuntimeOnly {
			caps = append(caps, &agentv1.Capability{Type: agentv1.Capability_RUNTIME})
		} else {
			caps = append(caps,
				&agentv1.Capability{Type: agentv1.Capability_BUILDER},
				&agentv1.Capability{Type: agentv1.Capability_RUNTIME},
			)
		}
	}
	if a.HotReload {
		caps = append(caps, &agentv1.Capability{Type: agentv1.Capability_HOT_RELOAD})
	}
	if a.ExecutionExporter {
		caps = append(caps, &agentv1.Capability{Type: agentv1.Capability_EXECUTION_EXPORTER})
	}

	info := &agentv1.AgentInformation{
		SupportedBackends:       a.Backends.ResolveBackends(),
		Capabilities:            caps,
		ReadMe:                  a.ReadMe,
		ConfigurationDetails:    a.Config,
		Techniques:              a.Techniques,
		Protocols:               []*agentv1.Protocol{},
		Validation:              a.Validation,
		EffectiveInputsVersions: a.EffectiveInputsVersions,
		UsageContract:           a.UsageContract,
	}
	if a.BackendsComplete {
		info.Contract = contract.Current()
		info.Contract.Capabilities = append(info.Contract.Capabilities, contract.RuntimeBackendDeclaration)
	}
	for _, t := range a.Toolchains {
		info.Toolchains = append(info.Toolchains, &agentv1.Toolchain{Type: t})
	}
	for _, l := range a.Languages {
		info.Languages = append(info.Languages, &agentv1.Language{Type: l})
	}
	for _, p := range a.Protocols {
		info.Protocols = append(info.Protocols, &agentv1.Protocol{Type: p})
	}
	return info
}
