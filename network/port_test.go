package network_test

import (
	"context"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/codefly-dev/core/network"
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

func getApp(num uint16) uint16 {
	return num / 1000
}

func TestPortGeneration(t *testing.T) {
	ctx := context.Background()
	// first 3 digits: app
	var appPart *uint16
	for i := 0; i < 10; i++ {
		app := gofakeit.AppName()
		for j := 0; j < 10; j++ {
			for _, api := range []string{standards.TCP, standards.HTTP, standards.GRPC} {
				svc := gofakeit.Adjective()
				v := network.ToNamedPort(ctx, "", app, svc, "test", api)
				require.GreaterOrEqual(t, v, uint16(11000))
				require.LessOrEqual(t, v, uint16(49999))
				if appPart == nil {
					appPart = new(uint16)
					*appPart = getApp(v)
				} else {
					require.Equal(t, *appPart, getApp(v))
				}
				require.Equal(t, network.APIInt(api), getLastDigit(int(v)))
			}
		}
		appPart = nil
	}
}
func TestPortDifferentAPIName(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "guestbook", "redis", standards.TCP, "read")
	two := network.ToNamedPort(ctx, "", "guestbook", "redis", standards.GRPC, "write")
	require.NotEqual(t, one, two)
}

func TestPortDifferentApp(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "counter-python-nextjs-postgres", "store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc")
	require.NotEqual(t, one, two)
}

func TestPortDifferentService(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "", "customers", "other-store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc")
	require.NotEqual(t, one, two)
}

func TestPortDifferent(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "other-", "customers", "store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "", "customers", "store", standards.TCP, "tpc")
	require.NotEqual(t, one, two)
}
