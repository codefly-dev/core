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

// ErrNoRuntimeBackend is what every refusal to plan a runtime context unwraps
// to, for a caller that only needs to know that one happened.
// NoRuntimeBackendError carries which services and whose fact refused them.
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
	complete := contract.Check(instance.Info.GetContract(), contract.RuntimeBackendDeclaration) == nil
	return backends, complete
}

// SubstitutionCause is why a planned runtime context is not the requested one.
// It separates the agent's declaration from the caller's own facts, because a
// run that reports one as the other sends the user to fix the wrong thing.
type SubstitutionCause int

const (
	// SubstitutionNone means the planned context is the requested one, or the
	// request was "free" and named no context to honour.
	SubstitutionNone SubstitutionCause = iota
	// SubstitutionUndeclared means the agent does not declare the requested
	// context among the backends it can use on this host. Only the agent can
	// change that.
	SubstitutionUndeclared
	// SubstitutionUnavailable means the agent DOES declare the requested
	// context, and the caller reported its backend unusable right now. The
	// remedy belongs to the caller, which owns that fact — core is never told
	// what made the backend unusable, so a diagnostic built here can only name
	// the backend, never the fix.
	SubstitutionUnavailable
)

// RuntimeContextRequest is one service's requested runtime context, paired with
// what its agent declared it can run as.
type RuntimeContextRequest struct {
	// Service names the service in diagnostics. Decisions come back aligned
	// with the requests, so nothing is keyed on it.
	Service string
	// Requested is the runtime context the run asked this service for.
	Requested string
	// Backends is the agent's declaration UNMODIFIED — Instance.DeclaredBackends
	// in its preference order. A caller that subtracts its own facts here makes
	// them indistinguishable from the agent's, and every diagnostic downstream
	// then blames the agent for the caller's state: pass Unavailable instead.
	Backends []agentv0.Backend_Type
	// Unavailable are declared backends the caller knows cannot be used right
	// now — a Docker engine that is installed but not running. Core reconciles
	// a declaration and probes nothing, so this is the only way host state
	// reaches the plan, and it stays attributable to the caller that supplied it.
	Unavailable []agentv0.Backend_Type
	// Complete is the second result of Instance.DeclaredBackends.
	Complete bool
}

// RuntimeContextDecision is the runtime context one service must run in.
// Decisions are returned aligned with the requests — decision i answers
// request i — so a caller zips them against the handles it already holds.
type RuntimeContextDecision struct {
	// Context is the runtime context to run the service in.
	Context string
	// Cause is why Context is not the requested one.
	Cause SubstitutionCause
	// LeavesContainerImage is true when the service will NOT run as a container
	// although its container image is what the run was heading for: the request
	// named "container", or the agent declares DOCKER and the caller reported it
	// unavailable. A stateful service then pairs on-disk state with whatever
	// engine version the host-native backend brings, which need not match the
	// one that wrote it — a data directory written by a postgres 16 image, read
	// by a nix postgres 17, fails later and opaquely. This is the one
	// substitution that is not merely slower or less isolated, so it is reported
	// apart from the rest rather than folded in with them.
	//
	// It does not see every instance of that hop. An agent whose
	// supported_backends omits DOCKER because the docker CLI is absent is
	// indistinguishable from one that ships no image at all, so a "free" run on
	// such a host is not flagged. Only the request and the caller's
	// unavailability report are visible here.
	LeavesContainerImage bool
}

// RuntimeContextRefusal is one service no runtime context could be planned for.
type RuntimeContextRefusal struct {
	// Service is the request's service.
	Service string
	// Unavailable are the backends the agent declared and the caller reported
	// unusable — the whole of its usable declaration, since one survivor would
	// have been planned. It is empty when the agent declared it can use no
	// backend here at all: that is the agent's own verdict and nothing the
	// caller can remedy, and the two cases are told apart by this field.
	Unavailable []agentv0.Backend_Type
}

// NoRuntimeBackendError names every service no runtime context could be planned
// for, and whose fact refused it, so a host can add the remedy it owns rather
// than discard this and build its own refusal from scratch. A host holding the
// reason a backend is unusable — which docker endpoint was unreachable, say —
// reaches these through errors.As and prints that beside them.
type NoRuntimeBackendError struct {
	// Unrunnable are the refused services, in request order.
	Unrunnable []RuntimeContextRefusal
}

func (e *NoRuntimeBackendError) Error() string {
	reasons := make([]string, 0, len(e.Unrunnable))
	for _, refusal := range e.Unrunnable {
		if len(refusal.Unavailable) == 0 {
			reasons = append(reasons, fmt.Sprintf("%s: its agent declares it can use no execution backend on this host", refusal.Service))
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s: its agent declares only %s, which the host reported unavailable",
			refusal.Service, strings.Join(runtimeContextsForBackends(refusal.Unavailable), ", ")))
	}
	return ErrNoRuntimeBackend.Error() + ": " + strings.Join(reasons, "; ")
}

func (e *NoRuntimeBackendError) Unwrap() error { return ErrNoRuntimeBackend }

// PlanRuntimeContexts reconciles every service's requested runtime context
// against its agent's declaration, before anything boots.
//
// A requested context is a preference, not an assertion each service of a graph
// can honour: one whose agent declared backends that do not include it runs in
// the agent's preferred declared backend instead. This is what lets
// `--runtime-context native` keep a container-only service in the run rather
// than taking the run down with it. Each decision says why it differs from the
// request and whether it gave up the service's container image, so a caller
// reports the hop that risks the service's state apart from the ones that cost
// nothing but isolation.
//
// A service left with no usable backend is refused in one NoRuntimeBackendError
// naming every such service, instead of each failing deep in its own Init with
// an error about a field the user never set.
//
// An agent that declared nothing and does not own its declaration is not
// judged — its request passes through, as it did before agents declared
// backends — while a non-empty declaration is read as complete whether or not
// the agent owns it: a list naming only DOCKER is already evidence that the
// plugin has no native path.
func PlanRuntimeContexts(requests []RuntimeContextRequest) ([]RuntimeContextDecision, error) {
	decisions := make([]RuntimeContextDecision, len(requests))
	var refused []RuntimeContextRefusal
	for i, request := range requests {
		if err := resources.ValidateRuntimeContext(request.Requested); err != nil {
			return nil, fmt.Errorf("%s: %w", request.Service, err)
		}
		usable, unusable := partitionBackends(request)
		if len(usable) == 0 {
			if len(unusable) == 0 && !request.Complete {
				decisions[i] = RuntimeContextDecision{Context: request.Requested}
				continue
			}
			refused = append(refused, RuntimeContextRefusal{Service: request.Service, Unavailable: unusable})
			continue
		}
		wanted := backendForRuntimeContext(request.Requested)
		if slices.Contains(usable, wanted) {
			decisions[i] = RuntimeContextDecision{Context: request.Requested}
			continue
		}
		decision := RuntimeContextDecision{Context: runtimeContextForBackend(usable[0])}
		if request.Requested != resources.RuntimeContextFree {
			decision.Cause = SubstitutionUndeclared
			if slices.Contains(unusable, wanted) {
				decision.Cause = SubstitutionUnavailable
			}
		}
		decision.LeavesContainerImage = decision.Context != resources.RuntimeContextContainer &&
			(request.Requested == resources.RuntimeContextContainer || slices.Contains(unusable, agentv0.Backend_DOCKER))
		decisions[i] = decision
	}
	if len(refused) > 0 {
		return nil, &NoRuntimeBackendError{Unrunnable: refused}
	}
	return decisions, nil
}

// partitionBackends splits the agent's declaration into the backends that can be
// used right now and those the caller reported unusable, preserving the agent's
// preference order. A backend this host has no runtime context for — an agent
// built against a newer schema — is dropped from both rather than guessed at.
func partitionBackends(request RuntimeContextRequest) (usable, unusable []agentv0.Backend_Type) {
	for _, backend := range request.Backends {
		if runtimeContextForBackend(backend) == "" {
			continue
		}
		if slices.Contains(request.Unavailable, backend) {
			unusable = append(unusable, backend)
			continue
		}
		usable = append(usable, backend)
	}
	return usable, unusable
}

// runtimeContextForBackend is the runtime context a service runs in on a
// backend, empty for a backend this host has no context for.
func runtimeContextForBackend(backend agentv0.Backend_Type) string {
	switch backend {
	case agentv0.Backend_LOCAL:
		return resources.RuntimeContextNative
	case agentv0.Backend_NIX:
		return resources.RuntimeContextNix
	case agentv0.Backend_DOCKER:
		return resources.RuntimeContextContainer
	default:
		return ""
	}
}

// backendForRuntimeContext is the backend a requested runtime context needs.
// "free" names none and accepts any, so it matches no declared backend and
// falls through to the agent's own preference.
func backendForRuntimeContext(runtimeContext string) agentv0.Backend_Type {
	switch runtimeContext {
	case resources.RuntimeContextNative:
		return agentv0.Backend_LOCAL
	case resources.RuntimeContextNix:
		return agentv0.Backend_NIX
	case resources.RuntimeContextContainer:
		return agentv0.Backend_DOCKER
	default:
		return agentv0.Backend_UNKNOWN
	}
}

// runtimeContextsForBackends names backends in the vocabulary the user typed on
// the command line, rather than the wire's.
func runtimeContextsForBackends(backends []agentv0.Backend_Type) []string {
	contexts := make([]string, 0, len(backends))
	for _, backend := range backends {
		if context := runtimeContextForBackend(backend); context != "" {
			contexts = append(contexts, context)
		}
	}
	return contexts
}
