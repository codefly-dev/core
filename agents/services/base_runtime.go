package services

import (
	"context"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/wool"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

type InformationStatus struct {
	Load  *runtimev0.LoadStatus
	Init  *runtimev0.InitStatus
	Start *runtimev0.StartStatus

	DesiredState *runtimev0.DesiredState
}

type RuntimeWrapper struct {
	*Base

	RuntimeContext *basev0.RuntimeContext

	RuntimeConfigurations []*basev0.Configuration

	LoadStatus    *runtimev0.LoadStatus
	InitStatus    *runtimev0.InitStatus
	StartStatus   *runtimev0.StartStatus
	StopStatus    *runtimev0.StopStatus
	DestroyStatus *runtimev0.DestroyStatus

	BuildStatus *runtimev0.BuildStatus
	TestStatus  *runtimev0.TestStatus
	LintStatus  *runtimev0.LintStatus

	DesiredState *runtimev0.DesiredState

	sync.RWMutex
}

// ── Load ──────────────────────────────────────────────────

func (s *RuntimeWrapper) LoadResponse() (*runtimev0.LoadResponse, error) {
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
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_ERROR, Message: err.Error()}
	return &runtimev0.LoadResponse{Status: s.LoadStatus}, err
}

func (s *RuntimeWrapper) LoadErrorf(err error, msg string, args ...any) (*runtimev0.LoadResponse, error) {
	s.LoadStatus = &runtimev0.LoadStatus{State: runtimev0.LoadStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.LoadResponse{Status: s.LoadStatus}, err
}

// ── Init ──────────────────────────────────────────────────

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
	return &runtimev0.InitResponse{Status: s.InitStatus}, err
}

func (s *RuntimeWrapper) InitErrorf(err error, msg string, args ...any) (*runtimev0.InitResponse, error) {
	s.InitStatus = &runtimev0.InitStatus{State: runtimev0.InitStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.InitResponse{Status: s.InitStatus}, err
}

// ── Start ─────────────────────────────────────────────────

func (s *RuntimeWrapper) StartResponse() (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_STARTED}
	return &runtimev0.StartResponse{Status: s.StartStatus}, nil
}

func (s *RuntimeWrapper) StartError(err error) (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_ERROR, Message: err.Error()}
	return &runtimev0.StartResponse{Status: s.StartStatus}, err
}

func (s *RuntimeWrapper) StartErrorf(err error, msg string, args ...any) (*runtimev0.StartResponse, error) {
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.StartResponse{Status: s.StartStatus}, err
}

// MarkRunnerExited records that the underlying runner process exited
// AFTER a successful Start. Plugins call this from a Wait-on-binary
// goroutine so the orchestrator's Follow() loop can observe the death
// (StartStatus → ERROR) and propagate the failure up to `codefly run`.
//
// Without this, fire-and-forget Start spawns leak: the binary dies,
// the plugin keeps running, codefly never learns, the user has dead
// children with a still-alive parent. See cli/cmd/run/service.go for
// the consumer side.
func (s *RuntimeWrapper) MarkRunnerExited(err error) {
	s.Lock()
	defer s.Unlock()
	msg := "runner exited"
	if err != nil {
		msg = err.Error()
	}
	s.StartStatus = &runtimev0.StartStatus{State: runtimev0.StartStatus_ERROR, Message: msg}
}

// ── Test ──────────────────────────────────────────────────

func (s *RuntimeWrapper) TestResponse() (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_SUCCESS}
	return &runtimev0.TestResponse{Status: s.TestStatus}, nil
}

func (s *RuntimeWrapper) TestResponseWithResults(run, passed, failed, skipped int32, coverage float32, failures []string, err error) (*runtimev0.TestResponse, error) {
	if err != nil || failed > 0 {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: msg}
	} else {
		s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_SUCCESS}
	}
	return &runtimev0.TestResponse{
		Status:       s.TestStatus,
		TestsRun:     run,
		TestsPassed:  passed,
		TestsFailed:  failed,
		TestsSkipped: skipped,
		CoveragePct:  coverage,
		Failures:     failures,
	}, err
}

func (s *RuntimeWrapper) TestError(err error) (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: err.Error()}
	return &runtimev0.TestResponse{Status: s.TestStatus}, err
}

func (s *RuntimeWrapper) TestErrorf(err error, msg string, args ...any) (*runtimev0.TestResponse, error) {
	s.TestStatus = &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.TestResponse{Status: s.TestStatus}, err
}

// ── Build ─────────────────────────────────────────────────

func (s *RuntimeWrapper) BuildResponse(output string) (*runtimev0.BuildResponse, error) {
	s.BuildStatus = &runtimev0.BuildStatus{State: runtimev0.BuildStatus_SUCCESS}
	return &runtimev0.BuildResponse{Status: s.BuildStatus, Output: output}, nil
}

func (s *RuntimeWrapper) BuildError(err error) (*runtimev0.BuildResponse, error) {
	s.BuildStatus = &runtimev0.BuildStatus{State: runtimev0.BuildStatus_ERROR, Message: err.Error()}
	return &runtimev0.BuildResponse{Status: s.BuildStatus}, err
}

func (s *RuntimeWrapper) BuildErrorf(err error, msg string, args ...any) (*runtimev0.BuildResponse, error) {
	s.BuildStatus = &runtimev0.BuildStatus{State: runtimev0.BuildStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.BuildResponse{Status: s.BuildStatus}, err
}

// ── Lint ──────────────────────────────────────────────────

func (s *RuntimeWrapper) LintResponse(output string) (*runtimev0.LintResponse, error) {
	s.LintStatus = &runtimev0.LintStatus{State: runtimev0.LintStatus_SUCCESS}
	return &runtimev0.LintResponse{Status: s.LintStatus, Output: output}, nil
}

func (s *RuntimeWrapper) LintError(err error) (*runtimev0.LintResponse, error) {
	s.LintStatus = &runtimev0.LintStatus{State: runtimev0.LintStatus_ERROR, Message: err.Error()}
	return &runtimev0.LintResponse{Status: s.LintStatus}, err
}

func (s *RuntimeWrapper) LintErrorf(err error, msg string, args ...any) (*runtimev0.LintResponse, error) {
	s.LintStatus = &runtimev0.LintStatus{State: runtimev0.LintStatus_ERROR, Message: ErrorMessage(err, msg, args...)}
	return &runtimev0.LintResponse{Status: s.LintStatus}, err
}

// ── Stop / Destroy ────────────────────────────────────────

func (s *RuntimeWrapper) StopResponse() (*runtimev0.StopResponse, error) {
	return &runtimev0.StopResponse{}, nil
}

func (s *RuntimeWrapper) StopError(err error) (*runtimev0.StopResponse, error) {
	s.StopStatus = &runtimev0.StopStatus{State: runtimev0.StopStatus_ERROR, Message: err.Error()}
	return &runtimev0.StopResponse{Status: s.StopStatus}, err
}

func (s *RuntimeWrapper) DestroyResponse() (*runtimev0.DestroyResponse, error) {
	return &runtimev0.DestroyResponse{}, nil
}

func (s *RuntimeWrapper) DestroyError(err error) (*runtimev0.DestroyResponse, error) {
	s.DestroyStatus = &runtimev0.DestroyStatus{State: runtimev0.DestroyStatus_ERROR, Message: err.Error()}
	return &runtimev0.DestroyResponse{Status: s.DestroyStatus}, err
}

// ── Information ───────────────────────────────────────────

func NOOP() *runtimev0.DesiredState {
	return &runtimev0.DesiredState{Stage: runtimev0.DesiredState_NOOP}
}

func (s *RuntimeWrapper) InformationResponse(_ context.Context, _ *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	s.Lock()
	defer s.Unlock()

	if s.DesiredState == nil {
		s.DesiredState = NOOP()
	}
	resp := &runtimev0.InformationResponse{
		LoadStatus:    s.LoadStatus,
		InitStatus:    s.InitStatus,
		StartStatus:   s.StartStatus,
		StopStatus:    s.StopStatus,
		DestroyStatus: s.DestroyStatus,
		TestStatus:    s.TestStatus,
		BuildStatus:   s.BuildStatus,
		LintStatus:    s.LintStatus,
		DesiredState:  s.DesiredState,
	}
	s.DesiredState = NOOP()
	return resp, nil
}

// ── Desired State ─────────────────────────────────────────

func (s *RuntimeWrapper) DesiredLoad() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{Stage: runtimev0.DesiredState_LOAD}
}

func (s *RuntimeWrapper) DesiredInit() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{Stage: runtimev0.DesiredState_INIT}
}

func (s *RuntimeWrapper) DesiredStart() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev0.DesiredState{Stage: runtimev0.DesiredState_START}
}

// ── Logging ───────────────────────────────────────────────

func (s *RuntimeWrapper) LogLoadRequest(req *runtimev0.LoadRequest) {
	s.Wool.In("runtime::load").Debug("input",
		wool.Field("environment", req.Environment),
		wool.Field("identity", req.Identity))
}

func (s *RuntimeWrapper) LogInitRequest(req *runtimev0.InitRequest) {
	s.Wool.In("runtime::init").Debug("input",
		wool.Field("runtime-context", req.RuntimeContext.Kind),
		wool.Field("configurations", resources.MakeConfigurationSummary(req.Configuration)),
		wool.Field("dependencies configurations", resources.MakeManyConfigurationSummary(req.DependenciesConfigurations)),
		wool.Field("dependency endpoints", resources.MakeManyEndpointSummary(req.DependenciesEndpoints)),
		wool.Field("network mapping", resources.MakeManyNetworkMappingSummary(req.ProposedNetworkMappings)))
}

func (s *RuntimeWrapper) LogStartRequest(req *runtimev0.StartRequest) {
	s.Wool.In("runtime::start").Debug("input",
		wool.Field("other network mappings", resources.MakeManyNetworkMappingSummary(req.DependenciesNetworkMappings)))
}

// ── Runtime Context ───────────────────────────────────────

func (s *RuntimeWrapper) IsContainerRuntime() bool {
	return s.RuntimeContext.Kind == resources.RuntimeContextContainer
}

func (s *RuntimeWrapper) IsNixRuntime() bool {
	return s.RuntimeContext.Kind == resources.RuntimeContextNix
}

func (s *RuntimeWrapper) IsNativeRuntime() bool {
	return s.RuntimeContext.Kind == resources.RuntimeContextNative
}

func (s *RuntimeWrapper) WithContext(runtimeContext *basev0.RuntimeContext) {
	s.RuntimeContext = runtimeContext
}

func (s *RuntimeWrapper) SetEnvironment(environment *basev0.Environment) {
	s.Environment = environment
	s.EnvironmentVariables.SetEnvironment(environment)
}
