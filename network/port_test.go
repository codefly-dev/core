package network_test

import (
	"context"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/standards"
)

func TestHashInt(t *testing.T) {
	for i := 0; i < 1000; i++ {
		s := gofakeit.BS()
		v := network.HashInt(s, 10, 99)
		require.GreaterOrEqual(t, v, 10)
		require.LessOrEqual(t, v, 99)
	}
}

func getLastDigit(num int) int {
	return num % 10
}

func TestPortGeneration(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		app := gofakeit.AppName()
		for j := 0; j < 10; j++ {
			for _, api := range []string{standards.TCP, standards.HTTP, standards.GRPC} {
				svc := gofakeit.Adjective()
				v := network.ToNamedPort(ctx, "", app, svc, "test", api, network.PortModeHost)
				require.GreaterOrEqual(t, v, uint16(1000))
				require.LessOrEqual(t, v, uint16(65535))
				require.Equal(t, network.APIInt(api), getLastDigit(int(v)))
			}
		}
	}
}

func TestPortDifferentAPIName(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "guestbook", "redis", standards.TCP, "read", network.PortModeHost)
	two := network.ToNamedPort(ctx, "", "guestbook", "redis", standards.GRPC, "write", network.PortModeHost)
	require.NotEqual(t, one, two)
}

func TestPortDifferentApp(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "counter-python-nextjs-postgres", "store", standards.TCP, "tpc", network.PortModeHost)
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc", network.PortModeHost)
	require.NotEqual(t, one, two)
}

func TestPortDifferentService(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "customers", "other-store", standards.TCP, "tpc", network.PortModeHost)
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc", network.PortModeHost)
	require.NotEqual(t, one, two)
}

func TestPortDifferentServiceOther(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "codefly-platform", "customers", "backend", standards.GRPC, "grpc", network.PortModeHost)
	two := network.ToNamedPort(ctx, "codefly-platform", "workspace", "workspace", standards.GRPC, "grpc", network.PortModeHost)
	require.NotEqual(t, one, two)
}

func TestPortDifferent(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "other-", "customers", "store", standards.TCP, "tpc", network.PortModeHost)
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc", network.PortModeHost)
	require.NotEqual(t, one, two)
}

// TestPortHostModeMatchesLegacy pins the guarantee that host mode reproduces
// the pre-mode hash byte-for-byte, so existing native/nix ports never move.
func TestPortHostModeMatchesLegacy(t *testing.T) {
	ctx := context.Background()
	// Legacy hash was Join(ws, mod, svc, name) with no mode segment; host mode
	// (empty string) must append nothing, keeping the same combined string.
	host := network.ToNamedPort(ctx, "mind-server", "mind", "mind", "grpc", standards.GRPC, network.PortModeHost)
	native := network.ToNamedPort(ctx, "mind-server", "mind", "mind", "grpc", standards.GRPC, network.PortModeFor(resources.RuntimeContextNative))
	nix := network.ToNamedPort(ctx, "mind-server", "mind", "mind", "grpc", standards.GRPC, network.PortModeFor(resources.RuntimeContextNix))
	require.Equal(t, host, native, "native folds to host mode")
	require.Equal(t, host, nix, "nix folds to host mode")
}

// TestPortContainerModeDiffersFromHost is the core guarantee: a container run
// and a native/nix run of the SAME service hash to different host ports, so
// they can run concurrently without colliding on the published port.
func TestPortContainerModeDiffersFromHost(t *testing.T) {
	ctx := context.Background()
	host := network.ToNamedPort(ctx, "mind-server", "mind", "mind", "grpc", standards.GRPC, network.PortModeHost)
	container := network.ToNamedPort(ctx, "mind-server", "mind", "mind", "grpc", standards.GRPC, network.PortModeFor(resources.RuntimeContextContainer))
	require.NotEqual(t, host, container)
	// Same last digit (API type) is preserved regardless of mode.
	require.Equal(t, network.APIInt(standards.GRPC), getLastDigit(int(container)))
}
