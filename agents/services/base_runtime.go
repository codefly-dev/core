package services

import (
	"context"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/wool"

	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
)

type RuntimeWrapper struct {
	*Base

	Environment *basev0.Environment

	RuntimeContext *basev0.RuntimeContext

	RuntimeConfigurations []*basev0.Configuration

	LoadStatus    *runtimev0.LoadStatus
	InitStatus    *runtimev0.InitStatus
	StartStatus   *runtimev0.StartStatus
	StopStatus    *runtimev0.StopStatus
	DestroyStatus *runtimev0.DestroyStatus

	TestStatus *runtimev0.TestStatus

	DesiredState *runtimev0.DesiredState

	sync.RWMutex
}

func (s *RuntimeWrapper) LoadResponse() (*runtimev0.LoadResponse, error) {
	// Validate
	if s.Environment == nil {
		return s.LoadError(s.Wool.NewError("environment is nil"))
	}
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_READY}
	s.Wool.Debug("load response", wool.NullableField("endpoints", resources.MakeManyEndpointSummary(s.Endpoints)))
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
	}, err
}

func (s *RuntimeWrapper) LoadErrorf(err error, msg string, args ...any) (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{
		State:   runtimev0.LoadStatus_ERROR,
		Message: ErrorMessage(err, msg, args...),
	}
	return &runtimev0.LoadResponse{
		Status: s.LoadStatus,
	}, err
}

func (s *RuntimeWrapper) InitResponse() (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_READY}
	return &runtimev0.InitResponse{
		Status:                s.InitStatus,
		NetworkMappings:       s.NetworkMappings,
		RuntimeConfigurations: s.RuntimeConfigurations,
	}, nil
}

func (s *RuntimeWrapper) InitError(err error) (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_ERROR, Message: err.Error()}

	return &runtimev0.InitResponse{
		Status: s.InitStatus,
	}, err
}

func (s *RuntimeWrapper) InitErrorf(err error, msg string, args ...any) (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_ERROR, Message: ErrorMessage(err, msg, args...)}

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

func (s *RuntimeWrapper) StartError(err error) (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_ERROR, Message: err.Error()}
	return &runtimev0.StartResponse{
		Status: s.StartStatus,
	}, err
}

func (s *RuntimeWrapper) StartErrorf(err error, msg string, args ...any) (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{
		State:   runtimev0.StartStatus_ERROR,
		Message: ErrorMessage(err, msg, args...),
	}
	return &runtimev0.StartResponse{
		Status: s.StartStatus,
	}, err
}

func (s *RuntimeWrapper) TestResponse() (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_SUCCESS}
	return &runtimev0.TestResponse{
		Status: s.TestStatus,
	}, nil
}

func (s *RuntimeWrapper) TestError(err error) (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: err.Error()}
	return &runtimev0.TestResponse{
		Status: s.TestStatus,
	}, err
}

func (s *RuntimeWrapper) TestErrorf(err error, msg string, args ...any) (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{
		State:   runtimev0.TestStatus_ERROR,
		Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.TestResponse{
		Status: s.TestStatus,
	}, err
}

func (s *RuntimeWrapper) StopResponse() (*runtimev0.StopResponse, error) {
	return &runtimev0.StopResponse{}, nil
}

func (s *RuntimeWrapper) StopError(err error) (*runtimev0.StopResponse, error) {
	s.StopStatus = &runtimev0.StopStatus{
		State: runtimev0.StopStatus_ERROR, Message: err.Error()}
	return &runtimev0.StopResponse{
		Status: s.StopStatus,
	}, err
}

func (s *RuntimeWrapper) StopErrorf(err error, msg string, args ...any) (*runtimev0.StopResponse, error) {
	s.StopStatus = &runtimev0.StopStatus{
		State: runtimev0.StopStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.StopResponse{
		Status: s.StopStatus,
	}, err
}

func (s *RuntimeWrapper) DestroyResponse() (*runtimev0.DestroyResponse, error) {
	return &runtimev0.DestroyResponse{}, nil
}

func (s *RuntimeWrapper) DestroyError(err error) (*runtimev0.DestroyResponse, error) {
	s.DestroyStatus = &runtimev0.DestroyStatus{
		State:   runtimev0.DestroyStatus_ERROR,
		Message: err.Error()}
	return &runtimev0.DestroyResponse{
		Status: s.DestroyStatus,
	}, err
}

func (s *RuntimeWrapper) DestroyErrorf(err error, msg string, args ...any) (*runtimev0.DestroyResponse, error) {
	s.DestroyStatus = &runtimev0.DestroyStatus{
		State:   runtimev0.DestroyStatus_ERROR,
		Message: ErrorMessage(err, msg, args...),
	}
	return &runtimev0.DestroyResponse{
		Status: s.DestroyStatus,
	}, err
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
		LoadStatus:    s.LoadStatus,
		InitStatus:    s.InitStatus,
		StartStatus:   s.StartStatus,
		StopStatus:    s.StopStatus,
		DestroyStatus: s.DestroyStatus,
		TestStatus:    s.TestStatus,
		DesiredState:  s.DesiredState,
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

func (s *RuntimeWrapper) LogLoadRequest(req *runtimev0.LoadRequest) {
	w := s.Wool.In("runtime::load")
	w.Debug("input",
		wool.Field("environment", req.Environment),
		wool.Field("identity", req.Identity))
}

func (s *RuntimeWrapper) LogInitRequest(req *runtimev0.InitRequest) {
	w := s.Wool.In("runtime::init")
	w.Debug("input",
		wool.Field("runtime-context", req.RuntimeContext.Kind),
		wool.Field("configurations", resources.MakeConfigurationSummary(req.Configuration)),
		wool.Field("dependencies configurations", resources.MakeManyConfigurationSummary(req.DependenciesConfigurations)),
		wool.Field("dependency endpoints", resources.MakeManyEndpointSummary(req.DependenciesEndpoints)),
		wool.Field("network mapping", resources.MakeManyNetworkMappingSummary(req.ProposedNetworkMappings)))
}

func (s *RuntimeWrapper) LogStartRequest(req *runtimev0.StartRequest) {
	w := s.Wool.In("runtime::start")
	w.Debug("input",
		wool.Field("other network mappings", resources.MakeManyNetworkMappingSummary(req.DependenciesNetworkMappings)),
	)
}

func (s *RuntimeWrapper) IsContainerRuntime() bool {
	return s.RuntimeContext.Kind == resources.RuntimeContextContainer

}

func (s *RuntimeWrapper) WithContext(runtimeContext *basev0.RuntimeContext) {
	s.RuntimeContext = runtimeContext
}

func (s *RuntimeWrapper) SetEnvironment(environment *basev0.Environment) {
	s.Environment = environment
	s.EnvironmentVariables.SetEnvironment(environment)
}
