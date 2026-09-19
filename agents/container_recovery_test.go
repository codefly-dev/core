package agents

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestAgentAcknowledgesInheritedContainerRecoveryOverGRPC(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "recoveryagent")
	output, err := exec.Command("go", "build", "-o", binary, "./testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	scope := recoveryscope.Marker(os.Getpid(), strings.Repeat("a", 64), strings.Repeat("b", 64))
	t.Setenv(recoveryscope.EnvironmentVariable, scope)
	for _, tc := range []struct {
		name, declaration, wantError string
		invalidScope                 bool
	}{
		{name: "default server declaration", declaration: "absent"},
		{name: "explicit declaration"},
		{name: "invalid scope", invalidScope: true},
		{name: "future declaration is preserved", declaration: "future", wantError: "host requires version 1"},
		{name: "zero declaration is preserved", declaration: "undeclared", wantError: "does not declare"},
		{name: "missing recovery is preserved", declaration: "no-recovery", wantError: "does not implement required capability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := scope
			if tc.invalidScope {
				marker = "0:invalid"
			}
			command := exec.Command(binary)
			command.Env = append(os.Environ(), "CODEFLY_AGENT_TOKEN=recovery-test", "CODEFLY_AGENT_UDS_PATH=", recoveryscope.EnvironmentVariable+"="+marker, "TEST_AGENT_CONTRACT="+tc.declaration)
			stdout, err := command.StdoutPipe()
			require.NoError(t, err)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			require.NoError(t, command.Start())
			t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
			endpoint := readHandshakeEndpoint(t, stdout, &stderr)
			conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			ctx = metadata.AppendToOutgoingContext(ctx, AuthMetadataKey, "recovery-test")
			var headers metadata.MD
			info, err := agentv0.NewAgentClient(conn).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{}, grpc.Header(&headers))
			require.NoError(t, err)
			checkErr := contract.Check(info.GetContract(), contract.ContainerRecoveryScope)
			if tc.wantError == "" {
				require.NoError(t, checkErr)
			} else {
				require.ErrorContains(t, checkErr, tc.wantError)
			}
			if tc.declaration != "absent" {
				require.Contains(t, info.GetContract().GetCapabilities(), "fixture-feature/v1")
			}
			again, err := agentv0.NewAgentClient(conn).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
			require.NoError(t, err)
			require.Equal(t, info.GetContract().GetCapabilities(), again.GetContract().GetCapabilities())

			if !tc.invalidScope {
				require.Equal(t, []string{recoveryscope.Acknowledgement()}, headers.Get(recoveryscope.Header))
			} else {
				require.Empty(t, headers.Get(recoveryscope.Header))
			}
		})
	}
}
