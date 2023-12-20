package configurations

import (
	"fmt"
)

/*
Forwarding is the such an important concept that it deserves to be part of the agent toolkit
*/

type Forwarding interface {
	Forward(r *RestRoute) (*RestRoute, error)
}

type ServiceForwarding struct {
	base *ServiceReference
	from *Service
}

var _ Forwarding = (*ServiceForwarding)(nil)

func (s ServiceForwarding) Forward(r *RestRoute) (*RestRoute, error) {
	return &RestRoute{
		Path:        fmt.Sprintf("/%s/%s%s", s.from.Application, s.from.Name, r.Path),
		Methods:     r.Methods,
		Application: s.base.Application,
		Service:     s.base.Name,
	}, nil
}
