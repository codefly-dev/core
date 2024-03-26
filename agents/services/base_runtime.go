package services

import (
	"context"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
)

type RuntimeWrapper struct {
	*Base

	Scope basev0.RuntimeScope

	ExposedConfigurations []*basev0.Configuration

	LoadStatus  *runtimev0.LoadStatus
	InitStatus  *runtimev0.InitStatus
	StartStatus *runtimev0.StartStatus
	StopStatus  *runtimev0.StopStatus

	DesiredState *runtimev0.DesiredState

	sync.RWMutex
}

func (s *RuntimeWrapper) LoadResponse() (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_READY}
	s.Wool.Debug("load response", wool.NullableField("endpoints", configurations.MakeManyEndpointSummary(s.Endpoints)))
	return &runtimev0.LoadResponse{
		Version:   s.Version(),
		Endpoints: s.Endpoints,
		Status:    s.LoadStatus,
	}, nil
}

func (s *RuntimeWrapper) LoadError(err error) (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{
		State:   runtimev0.LoadStatus_ERROR,
		Message: err.Error()}
	return &runtimev0.LoadResponse{
		Status: s.LoadStatus,
	}, nil
}

func (s *RuntimeWrapper) LoadErrorWithDetails(err error, details string) (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{
		State:   runtimev0.LoadStatus_ERROR,
		Message: err.Error(),
		Details: details,
	}
	return &runtimev0.LoadResponse{
		Status: s.LoadStatus,
	}, nil
}

func (s *RuntimeWrapper) InitResponse() (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_READY}
	return &runtimev0.InitResponse{
		Status:          s.InitStatus,
		NetworkMappings: s.NetworkMappings,
		Configurations:  s.ExportedConfigurations,
	}, nil
}

func (s *RuntimeWrapper) InitError(err error, fields ...*wool.LogField) (*runtimev0.InitResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_ERROR, Message: message.String()}

	return &runtimev0.InitResponse{
		Status: s.InitStatus,
	}, nil
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
	}, nil
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
	}, nil
}

func NOOP() *runtimev0.DesiredState {
	return &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_NOOP,
	}
}

func (s *RuntimeWrapper) InformationResponse(_ context.Context, _ *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	s.RLock()
	if s.DesiredState == nil {
		s.DesiredState = NOOP()
	}
	// After "read", we are back to normal state
	defer func() {
		s.RUnlock()
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
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_LOAD,
	}
}

func (s *RuntimeWrapper) DesiredInit() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_INIT,
	}
}

func (s *RuntimeWrapper) DesiredStart() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{
		Stage: runtimev0.DesiredState_START,
	}
}

func (s *RuntimeWrapper) NetworkInstance(mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) (*basev0.NetworkInstance, error) {
	return configurations.FindNetworkInstance(mappings, endpoint, s.Scope)
}
