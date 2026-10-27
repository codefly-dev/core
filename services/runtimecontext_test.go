package services

import (
	"errors"
	"testing"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func backends(types ...agentv0.Backend_Type) []*agentv0.Backend {
	out := make([]*agentv0.Backend, 0, len(types))
	for _, t := range types {
		out = append(out, &agentv0.Backend{Type: t})
	}
	return out
}

func owningContract() *agentv0.AgentContract {
	own := contract.Current()
	own.Capabilities = append(own.Capabilities, contract.RuntimeBackendDeclaration)
	return own
}

func TestDeclaredBackends(t *testing.T) {
	t.Run("an agent owning its declaration is complete", func(t *testing.T) {
		instance := &Instance{Info: &agentv0.AgentInformation{
			Contract:          owningContract(),
			SupportedBackends: backends(agentv0.Backend_DOCKER),
		}}
		declared, complete := instance.DeclaredBackends()
		require.Equal(t, []agentv0.Backend_Type{agentv0.Backend_DOCKER}, declared)
		require.True(t, complete)
	})
	t.Run("an empty declaration from an owning agent means nothing runs here", func(t *testing.T) {
		instance := &Instance{Info: &agentv0.AgentInformation{Contract: owningContract()}}
		declared, complete := instance.DeclaredBackends()
		require.Empty(t, declared)
		require.True(t, complete)
	})
	t.Run("the shared contract does not own the declaration", func(t *testing.T) {
		instance := &Instance{Info: &agentv0.AgentInformation{
			Contract:          contract.Current(),
			SupportedBackends: backends(agentv0.Backend_LOCAL),
		}}
		declared, complete := instance.DeclaredBackends()
		require.Equal(t, []agentv0.Backend_Type{agentv0.Backend_LOCAL}, declared)
		require.False(t, complete)
	})
	// Reading the capability must not depend on Identity: it is a question about
	// the advertisement, and routing it through an error constructor made it
	// deref a field it has no use for, on the path every agent takes today.
	t.Run("an agent declaring no contract is not complete, and needs no identity", func(t *testing.T) {
		instance := &Instance{Info: &agentv0.AgentInformation{}}
		declared, complete := instance.DeclaredBackends()
		require.Empty(t, declared)
		require.False(t, complete)
	})
}

func TestPlanRuntimeContexts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		request  RuntimeContextRequest
		decision RuntimeContextDecision
	}{
		{
			name:     "a container-only service runs as a container under native",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextContainer, Cause: SubstitutionUndeclared},
		},
		{
			// The hop the rest of this table must stay distinguishable from: the
			// service gives up the image that wrote its state.
			name:     "giving up a container for nix is flagged, not folded in with the rest",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextContainer, Backends: []agentv0.Backend_Type{agentv0.Backend_NIX}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNix, Cause: SubstitutionUndeclared, LeavesContainerImage: true},
		},
		{
			name:     "an unavailable docker engine is the caller's fact, not the agent's declaration",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextContainer, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_DOCKER}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative, Cause: SubstitutionUnavailable, LeavesContainerImage: true},
		},
		{
			name:     "an agent that never declared docker is the agent's own limit",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextContainer, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative, Cause: SubstitutionUndeclared, LeavesContainerImage: true},
		},
		{
			// The free resolution that already warns in the CLI: nothing asked
			// for a container, but the image is what the run was heading for.
			name:     "a free run that loses its docker engine still gives up an image",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextFree, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER, agentv0.Backend_NIX}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNix, LeavesContainerImage: true},
		},
		{
			// Cause and LeavesContainerImage answer different questions: the
			// agent never declared nix, AND the image it did declare is gone.
			name:     "a context the agent never declared can still give up an image",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNix, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER, agentv0.Backend_LOCAL}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative, Cause: SubstitutionUndeclared, LeavesContainerImage: true},
		},
		{
			// An honoured request gives up nothing by substitution: the user
			// named native themselves, so flagging it would make the flag noise.
			name:     "an honoured native request is not flagged when docker is down",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_DOCKER}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative},
		},
		{
			name:     "a declared context is honoured",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_NIX, agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative},
		},
		{
			name:     "an honoured container request gives up no image",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextContainer, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextContainer},
		},
		{
			name:     "free resolves to the agent's preferred backend",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextFree, Backends: []agentv0.Backend_Type{agentv0.Backend_NIX, agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNix},
		},
		{
			name:     "free resolving to a container is no substitution at all",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextFree, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextContainer},
		},
		{
			name:     "free stays free for an agent that declared nothing",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextFree},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextFree},
		},
		{
			name:     "an agent that declared nothing is not judged",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNix},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNix},
		},
		{
			name:     "a non-empty declaration is complete without the capability",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNix, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextContainer, Cause: SubstitutionUndeclared},
		},
		{
			name:     "a backend this host has no context for is dropped",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_UNKNOWN, agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextContainer, Cause: SubstitutionUndeclared},
		},
		{
			name:     "an unmappable declaration alone is not a verdict",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_UNKNOWN}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative},
		},
		{
			// An unavailability report naming a backend the agent never declared
			// changes nothing: the caller's facts narrow the declaration, never
			// widen it.
			name:     "unavailability outside the declaration is inert",
			request:  RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			decision: RuntimeContextDecision{Context: resources.RuntimeContextNative},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.request.Service = "documents/libreoffice"
			decisions, err := PlanRuntimeContexts([]RuntimeContextRequest{tc.request})
			require.NoError(t, err)
			require.Equal(t, []RuntimeContextDecision{tc.decision}, decisions)
		})
	}
}

func TestPlanRuntimeContextsRefusesBeforeAnythingBoots(t *testing.T) {
	_, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Complete: true},
		{Service: "app/web", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}},
		{Service: "infra/postgres", Requested: resources.RuntimeContextContainer, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}, Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
	})
	require.ErrorIs(t, err, ErrNoRuntimeBackend)

	// The host adds the remedy it owns — which endpoint was unreachable, how to
	// start it — so the refusals have to arrive as data, not only as prose.
	var refusal *NoRuntimeBackendError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, []RuntimeContextRefusal{
		{Service: "documents/libreoffice"},
		{Service: "infra/postgres", Unavailable: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
	}, refusal.Unrunnable)

	require.Contains(t, err.Error(), "documents/libreoffice: its agent declares it can use no execution backend on this host")
	require.Contains(t, err.Error(), "infra/postgres: its agent declares only container, which the host reported unavailable")
	require.NotContains(t, err.Error(), "app/web")
}

func TestPlanRuntimeContextsRefusesAnUnmappableCompleteDeclaration(t *testing.T) {
	_, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_UNKNOWN}, Complete: true},
	})
	require.ErrorIs(t, err, ErrNoRuntimeBackend)
	require.Contains(t, err.Error(), "can use no execution backend on this host")
}

func TestPlanRuntimeContextsRefusesAContextNoLocalRunHas(t *testing.T) {
	_, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextKubernetes},
	})
	require.ErrorContains(t, err, "documents/libreoffice: invalid runtime context: kubernetes")
	require.False(t, errors.Is(err, ErrNoRuntimeBackend))
}

func TestPlanRuntimeContextsAnswersAlignedWithItsRequests(t *testing.T) {
	decisions, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}, Complete: true},
		{Service: "app/web", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}, Complete: true},
		{Service: "infra/postgres", Requested: resources.RuntimeContextFree},
	})
	require.NoError(t, err)
	require.Equal(t, []RuntimeContextDecision{
		{Context: resources.RuntimeContextContainer, Cause: SubstitutionUndeclared},
		{Context: resources.RuntimeContextNative},
		{Context: resources.RuntimeContextFree},
	}, decisions)
}
