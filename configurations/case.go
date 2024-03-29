package configurations

import "github.com/codefly-dev/core/shared"

type ServiceWithCase struct {
	Name   shared.Case
	Unique shared.Case
}

func ToServiceWithCase(svc *Service) *ServiceWithCase {
	return &ServiceWithCase{
		Name:   shared.ToCase(svc.Name),
		Unique: shared.ToCase(svc.Unique()),
	}
}
