package services

import (
	"testing"

	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestAcknowledgedContainerRecoveryScope(t *testing.T) {
	header := dockerrun.ContainerRecoveryScopeHeader
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
