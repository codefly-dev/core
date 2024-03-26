package network

import (
	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type DNSManager interface {
	Get(service *configurations.Service, endpoint *configurations.Endpoint, env *configurations.Environment) (*basev0.DNS, error)
}
