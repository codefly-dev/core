package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkAccessFromRuntimeContext(t *testing.T) {
	require.Equal(t, NewNativeNetworkAccess().GetKind(), NetworkAccessFromRuntimeContext(nil).GetKind())
	require.Equal(t, NewNativeNetworkAccess().GetKind(), NetworkAccessFromRuntimeContext(NewRuntimeContextNix()).GetKind())
	require.Equal(t, NewContainerNetworkAccess().GetKind(), NetworkAccessFromRuntimeContext(NewRuntimeContextContainer()).GetKind())
}
