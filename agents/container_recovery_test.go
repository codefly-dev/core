package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestAgentAcknowledgesInheritedContainerRecoveryOverGRPC(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "recoveryagent")
	output, err := exec.Command("go", "build", "-o", binary, "./testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Setenv(dockerrun.ContainerRecoveryScopeEnvironment, "")
	scope, err := dockerrun.NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "test")
	require.NoError(t, err)
	require.NoError(t, dockerrun.SetContainerRecoveryScope(scope))
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			marker := os.Getenv(dockerrun.ContainerRecoveryScopeEnvironment)
			if !valid {
				marker = "0:invalid"
			}
			command := exec.Command(binary)
			command.Env = append(os.Environ(), "CODEFLY_AGENT_TOKEN=recovery-test", "CODEFLY_AGENT_UDS_PATH=", dockerrun.ContainerRecoveryScopeEnvironment+"="+marker)
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
			_, err = agentv0.NewAgentClient(conn).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{}, grpc.Header(&headers))
			require.NoError(t, err)
			if valid {
				require.Equal(t, []string{dockerrun.InheritedContainerRecoveryScope()}, headers.Get(dockerrun.ContainerRecoveryScopeHeader))
			} else {
				require.Empty(t, headers.Get(dockerrun.ContainerRecoveryScopeHeader))
			}
		})
	}
}
