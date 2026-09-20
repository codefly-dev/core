package contract

import (
	"encoding/hex"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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
		{name: "undeclared startup", advertised: &agentv0.AgentContract{ProtocolVersion: 1}, wantError: "startup protocol version 0; host requires version 2"},
		{name: "incompatible startup", advertised: &agentv0.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 3}, wantError: "startup protocol version 3; host requires version 2"},
		{name: "baseline needs no optional features", advertised: &agentv0.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 2}},
		{name: "recovery", advertised: Current(), required: []string{ContainerRecoveryScope}},
		{name: "missing recovery", advertised: &agentv0.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 2}, required: []string{ContainerRecoveryScope}, wantError: "does not implement required capability \"container-recovery-scope/v1\""},
		{name: "unknown extra feature", advertised: &agentv0.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 2, Capabilities: []string{"future-feature/v1"}}},
		{name: "unknown required feature", advertised: Current(), required: []string{"future-feature/v1"}, wantError: "future-feature/v1"},
		{name: "every requirement checked", advertised: Current(), required: []string{ContainerRecoveryScope, "future-feature/v1"}, wantError: "future-feature/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(tc.advertised, tc.required...)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.ErrorIs(t, err, ErrIncompatible)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCurrentIsIndependent(t *testing.T) {
	first := Current()
	require.Equal(t, uint32(1), first.ProtocolVersion)
	require.Equal(t, uint32(StartupProtocolVersion), first.StartupProtocolVersion)
	require.Contains(t, first.Capabilities, ContainerRecoveryScope)
	first.ProtocolVersion = 99
	first.Capabilities[0] = "changed"
	require.NoError(t, Check(Current(), ContainerRecoveryScope))
}

func TestPythonContractWire(t *testing.T) {
	wire, err := hex.DecodeString("62210801121b636f6e7461696e65722d7265636f766572792d73636f70652f76311802")
	require.NoError(t, err)
	var info agentv0.AgentInformation
	require.NoError(t, proto.Unmarshal(wire, &info))
	require.True(t, proto.Equal(Current(), info.Contract))
	encoded, err := proto.Marshal(&info)
	require.NoError(t, err)
	require.Equal(t, wire, encoded)
}
