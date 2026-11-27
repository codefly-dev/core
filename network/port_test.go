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

func TestNamedPortUsesEachIdentityComponent(t *testing.T) {
	ctx := t.Context()
	baseline := network.ToNamedPort(ctx, "workspace", "module", "service", "read", standards.TCP, network.PortModeHost)
	for _, tc := range []struct {
		name                                      string
		workspace, module, service, endpoint, api string
	}{
		{"workspace", "other", "module", "service", "read", standards.TCP},
		{"module", "workspace", "other", "service", "read", standards.TCP},
		{"service", "workspace", "module", "other", "read", standards.TCP},
		{"endpoint", "workspace", "module", "service", "write", standards.TCP},
		{"api", "workspace", "module", "service", "read", standards.GRPC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := network.ToNamedPort(ctx, tc.workspace, tc.module, tc.service, tc.endpoint, tc.api, network.PortModeHost)
			require.NotEqual(t, baseline, got)
		})
	}
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
	const legacyPort uint16 = 50843
	require.Equal(t, legacyPort, host)
	require.Equal(t, legacyPort, native, "native folds to host mode")
	require.Equal(t, legacyPort, nix, "nix folds to host mode")
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
