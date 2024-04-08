package network_test

import (
	"context"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/codefly-dev/core/network"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/stretchr/testify/assert"
)

func TestHashInt(t *testing.T) {
	for i := 0; i < 1000; i++ {
		s := gofakeit.BS()
		v := network.HashInt(s, 10, 99)
		assert.GreaterOrEqual(t, v, 10)
		assert.LessOrEqual(t, v, 99)
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
				v := network.ToNamedPort(ctx, "project", app, svc, "test", api)
				assert.GreaterOrEqual(t, v, uint16(11000))
				assert.LessOrEqual(t, v, uint16(49999))
				if appPart == nil {
					appPart = new(uint16)
					*appPart = getApp(v)
				} else {
					assert.Equal(t, *appPart, getApp(v))
				}
				assert.Equal(t, network.APIInt(api), getLastDigit(int(v)))
			}
		}
		appPart = nil
	}
}
func TestPortDifferentAPIName(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "project", "guestbook", "redis", standards.TCP, "read")
	two := network.ToNamedPort(ctx, "project", "guestbook", "redis", standards.GRPC, "write")
	assert.NotEqual(t, one, two)
}

func TestPortDifferentApp(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "project", "counter-python-nextjs-postgres", "store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "project", "customers", "store", standards.TCP, "tpc")
	assert.NotEqual(t, one, two)
}

func TestPortDifferentService(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "project", "customers", "other-store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "project", "customers", "store", standards.TCP, "tpc")
	assert.NotEqual(t, one, two)
}

func TestPortDifferentProject(t *testing.T) {
	ctx := context.Background()
	one := network.ToNamedPort(ctx, "other-project", "customers", "store", standards.TCP, "tpc")
	two := network.ToNamedPort(ctx, "project", "customers", "store", standards.TCP, "tpc")
	assert.NotEqual(t, one, two)
}
