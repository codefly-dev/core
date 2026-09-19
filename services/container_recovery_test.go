package services

import (
	"testing"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestAcknowledgedContainerRecoveryScope(t *testing.T) {
	header := recoveryscope.Header
	t.Run("absent", func(t *testing.T) {
		scope, err := acknowledgedContainerRecoveryScope(metadata.MD{})
		require.NoError(t, err)
		require.Empty(t, scope)
	})
	t.Run("single", func(t *testing.T) {
		scope, err := acknowledgedContainerRecoveryScope(metadata.Pairs(header, "scope:namespace"))
		require.NoError(t, err)
		require.Equal(t, "scope:namespace", scope)
	})
	t.Run("empty namespace is still an acknowledgement", func(t *testing.T) {
		scope, err := acknowledgedContainerRecoveryScope(metadata.Pairs(header, "scope:"))
		require.NoError(t, err)
		require.Equal(t, "scope:", scope)
	})
	t.Run("two identities are refused, not silently dropped", func(t *testing.T) {
		scope, err := acknowledgedContainerRecoveryScope(metadata.Pairs(header, "one", header, "two"))
		require.ErrorContains(t, err, "acknowledged 2 container recovery identities")
		require.Empty(t, scope)
	})
}

func TestRequireContainerRecoveryScope(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		advertised               *agentv0.AgentContract
		ack, expected, wantError string
	}{
		{name: "supported and acknowledged", advertised: contract.Current(), ack: "scope:namespace", expected: "scope:namespace"},
		{name: "optional namespace", advertised: contract.Current(), ack: "scope:", expected: "scope:"},
		{name: "legacy acknowledgement does not imply support", ack: "scope:namespace", expected: "scope:namespace", wantError: "does not declare"},
		{name: "capability missing", advertised: &agentv0.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 2}, expected: "scope:namespace", wantError: "does not implement required capability \"container-recovery-scope/v1\""},
		{name: "supported but unacknowledged", advertised: contract.Current(), expected: "scope:namespace", wantError: "implements container-recovery-scope/v1 but did not acknowledge"},
		{name: "wrong run", advertised: contract.Current(), ack: "other:namespace", expected: "scope:namespace", wantError: "did not acknowledge"},
		{name: "no resolved scope", advertised: contract.Current(), wantError: "requires a resolved scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := &Instance{
				Identity:               &resources.ServiceIdentity{Module: "module", Name: "service"},
				Info:                   &agentv0.AgentInformation{Contract: tc.advertised},
				ContainerRecoveryScope: tc.ack,
			}
			err := instance.RequireContainerRecoveryScope(tc.expected)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.ErrorContains(t, err, "module/service")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
