package contract

import (
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name       string
		advertised *agentv0.AgentContract
		required   []string
		wantError  string
	}{
		{name: "undeclared", wantError: "does not declare"},
		{name: "zero", advertised: &agentv0.AgentContract{}, wantError: "does not declare"},
		{name: "future protocol", advertised: &agentv0.AgentContract{ProtocolVersion: 2}, wantError: "version 2; host requires version 1"},
		{name: "baseline needs no optional features", advertised: &agentv0.AgentContract{ProtocolVersion: 1}},
		{name: "recovery", advertised: Current(), required: []string{ContainerRecoveryScope}},
		{name: "missing recovery", advertised: &agentv0.AgentContract{ProtocolVersion: 1}, required: []string{ContainerRecoveryScope}, wantError: "does not implement required capability \"container-recovery-scope/v1\""},
		{name: "unknown extra feature", advertised: &agentv0.AgentContract{ProtocolVersion: 1, Capabilities: []string{"future-feature/v1"}}},
		{name: "unknown required feature", advertised: Current(), required: []string{"future-feature/v1"}, wantError: "future-feature/v1"},
		{name: "every requirement checked", advertised: Current(), required: []string{ContainerRecoveryScope, "future-feature/v1"}, wantError: "future-feature/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(tc.advertised, tc.required...)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCurrentIsIndependent(t *testing.T) {
	first := Current()
	require.Equal(t, uint32(1), first.ProtocolVersion)
	require.Contains(t, first.Capabilities, ContainerRecoveryScope)
	first.ProtocolVersion = 99
	first.Capabilities[0] = "changed"
	require.NoError(t, Check(Current(), ContainerRecoveryScope))
}
