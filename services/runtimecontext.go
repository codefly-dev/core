package services

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
)

// ErrNoRuntimeBackend means an agent that owns its backend declaration
// advertised none: nothing it can run exists on this host, so there is no
// runtime context to substitute for the one the run asked for.
var ErrNoRuntimeBackend = errors.New("no runtime backend available")

// DeclaredBackends returns the execution backends the agent advertised for this
// host, in its own preference order — element 0 is the one it prefers — and
// whether that declaration is complete, which is its
// runtime-backend-declaration/v1 capability.
//
// Completeness is the difference between an agent that can run nowhere here and
// an agent that said nothing; on the wire both are an empty repeated field.
func (instance *Instance) DeclaredBackends() ([]agentv0.Backend_Type, bool) {
	declared := instance.Info.GetSupportedBackends()
	backends := make([]agentv0.Backend_Type, 0, len(declared))
	for _, backend := range declared {
		backends = append(backends, backend.GetType())
	}
	return backends, instance.RequireAgentCapabilities(contract.RuntimeBackendDeclaration) == nil
}

// RuntimeContextRequest is one service's requested runtime context, paired with
// what its agent declared it can run as.
type RuntimeContextRequest struct {
	// Service names the service in diagnostics. The plan answers in request
	// order, so nothing is keyed on it.
	Service string
	// Requested is the runtime context the run asked this service for.
	Requested string
	// Backends are the agent's declared execution backends in its preference
	// order: Instance.DeclaredBackends, less any backend the caller knows is
	// unusable right now — a Docker engine that is installed but not running.
	Backends []agentv0.Backend_Type
	// Complete is the second result of Instance.DeclaredBackends.
	Complete bool
}

// RuntimeContextDecision is the runtime context one service must run in.
type RuntimeContextDecision struct {
	// Service is the request's service.
	Service string
	// Requested is the runtime context the run asked for.
	Requested string
	// Context is the runtime context to run the service in.
	Context string
	// Substituted is true when the agent declared it cannot run in Requested,
	// so Context is its preferred backend instead. A substitution is the run
	// honouring a preference it cannot apply literally, not a failure; a run
	// reports them once, plainly.
	Substituted bool
}

// PlanRuntimeContexts reconciles every service's requested runtime context
// against its agent's declaration, before anything boots.
//
// A requested context is a preference, not an assertion each service of a graph
// can honour: one whose agent declared backends that do not include it runs in
// the agent's preferred declared backend instead, reported as a substitution.
// This is what lets `--runtime-context native` keep a container-only service in
// the run rather than taking the run down with it.
//
// A service whose agent owns its declaration and declared no backend can run
// nowhere on this host. Every such service is named in one ErrNoRuntimeBackend,
// instead of each failing deep in its own Init with an error about a field the
// user never set.
//
// An agent that declared nothing and does not own its declaration is not
// judged — its request passes through, as it did before agents declared
// backends — while a non-empty declaration is read as complete whether or not
// the agent owns it: a list naming only DOCKER is already evidence that the
// plugin has no native path.
func PlanRuntimeContexts(requests []RuntimeContextRequest) ([]RuntimeContextDecision, error) {
	decisions := make([]RuntimeContextDecision, 0, len(requests))
	var unrunnable []string
	for _, request := range requests {
		if err := resources.ValidateRuntimeContext(request.Requested); err != nil {
			return nil, fmt.Errorf("%s: %w", request.Service, err)
		}
		supported := declaredRuntimeContexts(request.Backends)
		decision := RuntimeContextDecision{
			Service:   request.Service,
			Requested: request.Requested,
			Context:   request.Requested,
		}
		switch {
		case len(supported) == 0:
			if request.Complete {
				unrunnable = append(unrunnable, request.Service)
				continue
			}
		case request.Requested == resources.RuntimeContextFree:
			decision.Context = supported[0]
		case !slices.Contains(supported, request.Requested):
			decision.Context = supported[0]
			decision.Substituted = true
		}
		decisions = append(decisions, decision)
	}
	if len(unrunnable) > 0 {
		return nil, fmt.Errorf("%w: %s declared no execution backend this host can use, so no runtime context substitutes for the one requested",
			ErrNoRuntimeBackend, strings.Join(unrunnable, ", "))
	}
	return decisions, nil
}

// declaredRuntimeContexts translates declared backends to the runtime contexts
// they run a service in, preserving the agent's preference order. A backend
// this host has no context for — an agent built against a newer schema — is
// dropped rather than guessed at.
func declaredRuntimeContexts(backends []agentv0.Backend_Type) []string {
	contexts := make([]string, 0, len(backends))
	for _, backend := range backends {
		switch backend {
		case agentv0.Backend_LOCAL:
			contexts = append(contexts, resources.RuntimeContextNative)
		case agentv0.Backend_NIX:
			contexts = append(contexts, resources.RuntimeContextNix)
		case agentv0.Backend_DOCKER:
			contexts = append(contexts, resources.RuntimeContextContainer)
		}
	}
	return contexts
}
