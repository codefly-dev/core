package network

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type DNSManager interface {
	GetDNS(ctx context.Context, svc *configurations.Service, endpointName string) (*basev0.DNS, error)
}
