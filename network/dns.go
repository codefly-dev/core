package network

import (
	"context"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	"github.com/codefly-dev/core/resources"
)

type DNSManager interface {
	GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev1.DNS, error)
}
