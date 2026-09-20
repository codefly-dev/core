//go:build published_agents_required

package services

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestPublishedAgentsDeclareRuntimeContract(t *testing.T) {
	require.NotEmpty(t, os.Getenv(resources.CodeflyHomeEnv), "set CODEFLY_HOME to an isolated cache populated using codefly agent install")
	installed, err := manager.Installed(t.Context(), resources.ServiceAgent)
	require.NoError(t, err)
	require.NotEmpty(t, installed, "install the published qualification selections first")
	for _, selected := range installed {
		t.Run(selected.Identifier(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			resolved, info, err := InspectAgent(ctx, selected)
			require.NoError(t, err)
			require.Equal(t, selected, resolved)
			t.Logf("protocol=%d startup=%d capabilities=%v", info.Contract.ProtocolVersion, info.Contract.StartupProtocolVersion, info.Contract.Capabilities)
		})
	}
}
