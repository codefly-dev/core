package network

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/resources"
)

type DNSManager interface {
	GetDNS(ctx context.Context, svc *resources.Service, endpointName string) (*basev0.DNS, error)
}
