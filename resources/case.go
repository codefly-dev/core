package resources

import "github.com/codefly-dev/core/shared"

type ServiceWithCase struct {
	Name   shared.Case
	Unique shared.Case
}

func ToServiceWithCase(svc *ServiceIdentity) *ServiceWithCase {
	return &ServiceWithCase{
		Name:   shared.ToCase(svc.Name),
		Unique: shared.ToCase(svc.Unique()),
	}
}

type ModuleWithCase struct {
	Name shared.Case
}

func ToModuleWithCase(svc *ServiceIdentity) *ModuleWithCase {
	return &ModuleWithCase{
		Name: shared.ToCase(svc.Module),
	}
}
