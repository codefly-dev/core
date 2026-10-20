package services

import (
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

func TestDeclaredBackends(t *testing.T) {
	identity := &resources.ServiceIdentity{Module: "documents", Name: "libreoffice"}
	t.Run("an agent owning its declaration is complete", func(t *testing.T) {
		own := contract.Current()
		own.Capabilities = append(own.Capabilities, contract.RuntimeBackendDeclaration)
		instance := &Instance{Identity: identity, Info: &agentv0.AgentInformation{
			Contract:          own,
			SupportedBackends: backends(agentv0.Backend_DOCKER),
		}}
		declared, complete := instance.DeclaredBackends()
		require.Equal(t, []agentv0.Backend_Type{agentv0.Backend_DOCKER}, declared)
		require.True(t, complete)
	})
	t.Run("an empty declaration from an owning agent means nothing runs here", func(t *testing.T) {
		own := contract.Current()
		own.Capabilities = append(own.Capabilities, contract.RuntimeBackendDeclaration)
		instance := &Instance{Identity: identity, Info: &agentv0.AgentInformation{Contract: own}}
		declared, complete := instance.DeclaredBackends()
		require.Empty(t, declared)
		require.True(t, complete)
	})
	t.Run("the shared contract does not own the declaration", func(t *testing.T) {
		instance := &Instance{Identity: identity, Info: &agentv0.AgentInformation{
			Contract:          contract.Current(),
			SupportedBackends: backends(agentv0.Backend_LOCAL),
		}}
		declared, complete := instance.DeclaredBackends()
		require.Equal(t, []agentv0.Backend_Type{agentv0.Backend_LOCAL}, declared)
		require.False(t, complete)
	})
	t.Run("an agent declaring no contract is not complete", func(t *testing.T) {
		instance := &Instance{Identity: identity, Info: &agentv0.AgentInformation{}}
		declared, complete := instance.DeclaredBackends()
		require.Empty(t, declared)
		require.False(t, complete)
	})
}

func TestPlanRuntimeContexts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		request     RuntimeContextRequest
		context     string
		substituted bool
	}{
		{
			name:        "a container-only service runs as a container under native",
			request:     RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			context:     resources.RuntimeContextContainer,
			substituted: true,
		},
		{
			name:    "a declared context is honoured",
			request: RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_NIX, agentv0.Backend_DOCKER}},
			context: resources.RuntimeContextNative,
		},
		{
			name:    "free resolves to the agent's preferred backend",
			request: RuntimeContextRequest{Requested: resources.RuntimeContextFree, Backends: []agentv0.Backend_Type{agentv0.Backend_NIX, agentv0.Backend_DOCKER}},
			context: resources.RuntimeContextNix,
		},
		{
			name:    "free stays free for an agent that declared nothing",
			request: RuntimeContextRequest{Requested: resources.RuntimeContextFree},
			context: resources.RuntimeContextFree,
		},
		{
			name:    "an agent that declared nothing is not judged",
			request: RuntimeContextRequest{Requested: resources.RuntimeContextNix},
			context: resources.RuntimeContextNix,
		},
		{
			name:        "a non-empty declaration is complete without the capability",
			request:     RuntimeContextRequest{Requested: resources.RuntimeContextNix, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}},
			context:     resources.RuntimeContextContainer,
			substituted: true,
		},
		{
			name:        "a backend this host has no context for is dropped",
			request:     RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_UNKNOWN, agentv0.Backend_DOCKER}},
			context:     resources.RuntimeContextContainer,
			substituted: true,
		},
		{
			name:    "an unmappable declaration alone is not a verdict",
			request: RuntimeContextRequest{Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_UNKNOWN}},
			context: resources.RuntimeContextNative,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.request.Service = "documents/libreoffice"
			decisions, err := PlanRuntimeContexts([]RuntimeContextRequest{tc.request})
			require.NoError(t, err)
			require.Equal(t, []RuntimeContextDecision{{
				Service:     "documents/libreoffice",
				Requested:   tc.request.Requested,
				Context:     tc.context,
				Substituted: tc.substituted,
			}}, decisions)
		})
	}
}

func TestPlanRuntimeContextsRefusesBeforeAnythingBoots(t *testing.T) {
	_, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Complete: true},
		{Service: "app/web", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}},
		{Service: "documents/xlsx", Requested: resources.RuntimeContextContainer, Complete: true},
	})
	require.ErrorIs(t, err, ErrNoRuntimeBackend)
	require.ErrorContains(t, err, "documents/libreoffice, documents/xlsx")
	require.NotContains(t, err.Error(), "app/web")
}

func TestPlanRuntimeContextsRefusesAContextNoLocalRunHas(t *testing.T) {
	_, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextKubernetes},
	})
	require.ErrorContains(t, err, "documents/libreoffice: invalid runtime context: kubernetes")
}

func TestPlanRuntimeContextsAnswersInRequestOrder(t *testing.T) {
	decisions, err := PlanRuntimeContexts([]RuntimeContextRequest{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_DOCKER}, Complete: true},
		{Service: "app/web", Requested: resources.RuntimeContextNative, Backends: []agentv0.Backend_Type{agentv0.Backend_LOCAL}, Complete: true},
	})
	require.NoError(t, err)
	require.Equal(t, []RuntimeContextDecision{
		{Service: "documents/libreoffice", Requested: resources.RuntimeContextNative, Context: resources.RuntimeContextContainer, Substituted: true},
		{Service: "app/web", Requested: resources.RuntimeContextNative, Context: resources.RuntimeContextNative},
	}, decisions)
}
