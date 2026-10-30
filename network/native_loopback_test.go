package network_test

import (
	"net"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

// Native mappings must use the same address family as the IPv4 loopback
// publication used by local containers. A hostname can resolve to IPv6, where
// Docker Desktop can reset the connection instead of allowing IPv4 fallback.
func TestNativeMappingMatchesIPv4LoopbackPublication(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	for _, api := range []string{standards.TCP, standards.GRPC, standards.HTTP, standards.CONNECT} {
		t.Run(api, func(t *testing.T) {
			instance := network.Native(&basev0.Endpoint{Api: api}, port)
			ip := net.ParseIP(instance.Hostname)
			require.NotNil(t, ip, "native publication must not depend on hostname address-family selection")
			require.NotNil(t, ip.To4())
			require.True(t, ip.IsLoopback())
			connection, err := net.Dial("tcp", instance.Host)
			require.NoError(t, err)
			require.NoError(t, connection.Close())
		})
	}
}
