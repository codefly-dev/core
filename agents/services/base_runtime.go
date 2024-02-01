package services

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
)

type RuntimeWrapper struct {
	*Base

	LoadStatus  *runtimev0.LoadStatus
	InitStatus  *runtimev0.InitStatus
	StartStatus *runtimev0.StartStatus
	StopStatus  *runtimev0.StopStatus

	DesiredState *runtimev0.DesiredState
}

func (s *RuntimeWrapper) LoadResponse() (*runtimev0.LoadResponse, error) {
	// for convenience, add application and service
	for _, endpoint := range s.Endpoints {
		endpoint.Application = s.Configuration.Application
		endpoint.Service = s.Configuration.Name
	}
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_READY}
	s.Wool.Debug("load response", wool.NullableField("exposing endpoints", configurations.MakeEndpointSummary(s.Endpoints)))
	return &runtimev0.LoadResponse{
		Version:   s.Version(),
		Endpoints: s.Endpoints,
		Status:    s.LoadStatus,
	}, nil
}

func (s *RuntimeWrapper) LoadError(err error) (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_ERROR, Message: err.Error()}
	return &runtimev0.LoadResponse{
		Status: s.LoadStatus,
	}, err
}

func (s *RuntimeWrapper) InitResponse(infos ...*basev0.ProviderInformation) (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_READY}
	return &runtimev0.InitResponse{
		Status:               s.InitStatus,
		ServiceProviderInfos: infos,
	}, nil
}

func (s *RuntimeWrapper) InitError(err error, fields ...*wool.LogField) (*runtimev0.InitResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_ERROR, Message: message.String()}

	return &runtimev0.InitResponse{
		Status: s.InitStatus,
	}, err
}

func (s *RuntimeWrapper) StartResponse() (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_STARTED}
	return &runtimev0.StartResponse{
		Status: s.StartStatus,
	}, nil
}

func (s *RuntimeWrapper) StartError(err error, fields ...*wool.LogField) (*runtimev0.StartResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_ERROR, Message: message.String()}
	return &runtimev0.StartResponse{
		Status: s.StartStatus,
	}, err
}

func (s *RuntimeWrapper) StopResponse() (*runtimev0.StopResponse, error) {
	return &runtimev0.StopResponse{}, nil
}

func (s *RuntimeWrapper) StopError(err error, fields ...*wool.LogField) (*runtimev0.StopResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	s.StopStatus = &runtimev0.StopStatus{State: runtimev0.StopStatus_ERROR, Message: message.String()}
	return &runtimev0.StopResponse{
		Status: s.StopStatus,
	}, err
}

func NOOP() *runtimev0.DesiredState {
	return &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_NOOP,
	}
}

func (s *RuntimeWrapper) InformationResponse(_ context.Context, _ *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	if s.DesiredState == nil {
		s.DesiredState = NOOP()
	}
	// After "read", we are back to normal state
	defer func() {
		s.DesiredState = NOOP()
	}()
	resp := &runtimev0.InformationResponse{
		LoadStatus:   s.LoadStatus,
		InitStatus:   s.InitStatus,
		StartStatus:  s.StartStatus,
		StopStatus:   s.StopStatus,
		DesiredState: s.DesiredState,
	}
	return resp, nil
}

func (s *RuntimeWrapper) DesiredLoad() {
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_LOAD,
	}
}

func (s *RuntimeWrapper) DesiredInit() {
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_INIT,
	}
}

func (s *RuntimeWrapper) DesiredStart() {
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_START,
	}
}
