package services

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/codefly-dev/core/builders"
	codeflyfailures "github.com/codefly-dev/core/failures"
	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/wool"

	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
)

type InformationStatus struct {
	Load  *runtimev1.LoadStatus
	Init  *runtimev1.InitStatus
	Start *runtimev1.StartStatus

	DesiredState *runtimev1.DesiredState
}

type RuntimeWrapper struct {
	*Base

	RuntimeContext *basev1.RuntimeContext

	RuntimeConfigurations []*basev1.Configuration

	LoadStatus    *runtimev1.LoadStatus
	InitStatus    *runtimev1.InitStatus
	StartStatus   *runtimev1.StartStatus
	StopStatus    *runtimev1.StopStatus
	DestroyStatus *runtimev1.DestroyStatus

	BuildStatus *runtimev1.BuildStatus
	TestStatus  *runtimev1.TestStatus
	LintStatus  *runtimev1.LintStatus

	DesiredState *runtimev1.DesiredState

	// generation counts successful Starts. It is stamped on every StartStatus
	// and health report so a consumer cannot read a ready result recorded before
	// a restart as evidence about the process running now.
	generation uint64
	health     *basev1.HealthReport
	// startedArtifact is what this agent reported it started for the start in
	// flight. StartResponse stamps it on the StartStatus — so a consumer reads
	// the same answer from Start and from Information — and clears it, because
	// it describes one process and the next start is another.
	startedArtifact *runtimev1.StartedArtifact

	sync.RWMutex
}

// RuntimeLoad describes a conventional plugin Runtime.Load implementation.
// ResolveEndpoints selects the endpoints the plugin needs after core loads the
// service declaration.
type RuntimeLoad struct {
	Settings         any
	Requirements     *builders.Dependencies
	ResolveEndpoints func(context.Context, []*basev1.Endpoint) error
}

// LoadService implements the conventional service runtime Load lifecycle.
func (s *RuntimeWrapper) LoadService(ctx context.Context, req *runtimev1.LoadRequest, load RuntimeLoad) (*runtimev1.LoadResponse, error) {
	if req == nil || req.GetIdentity() == nil {
		return s.LoadError(s.Wool.NewError("runtime load requires a service identity"))
	}
	s.LogLoadRequest(req)
	if err := s.Base.Load(ctx, req.GetIdentity(), load.Settings); err != nil {
		return s.LoadErrorf(err, "loading base")
	}
	ctx = s.Wool.Inject(ctx)
	if req.GetDisableCatch() {
		s.Wool.DisableCatch()
	}
	s.SetEnvironment(req.GetEnvironment())
	if load.Requirements != nil {
		load.Requirements.Localize(s.Location)
	}
	endpoints, err := s.Service.LoadEndpoints(ctx)
	if err != nil {
		return s.LoadErrorf(err, "cannot load endpoints")
	}
	s.Endpoints = endpoints
	if load.ResolveEndpoints != nil {
		if err = load.ResolveEndpoints(ctx, endpoints); err != nil {
			return s.LoadError(err)
		}
	}
	return s.LoadResponse()
}

// ── Load ──────────────────────────────────────────────────

func (s *RuntimeWrapper) LoadResponse() (*runtimev1.LoadResponse, error) {
	if s.Environment == nil {
		return s.LoadError(s.Wool.NewError("environment is nil"))
	}
	s.Lock()
	s.LoadStatus = &runtimev1.LoadStatus{State: runtimev1.LoadStatus_READY}
	s.Unlock()
	s.Wool.Debug("load response", wool.NullableField("endpoints", resources.MakeManyEndpointSummary(s.Endpoints)))
	return &runtimev1.LoadResponse{
		Version:   s.Version(),
		Endpoints: s.Endpoints,
		Status:    s.LoadStatus,
	}, nil
}

func (s *RuntimeWrapper) LoadError(err error) (*runtimev1.LoadResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.LoadStatus = &runtimev1.LoadStatus{State: runtimev1.LoadStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.load", err, err.Error())}
	return &runtimev1.LoadResponse{Status: s.LoadStatus}, nil
}

func (s *RuntimeWrapper) LoadErrorf(err error, msg string, args ...any) (*runtimev1.LoadResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := ErrorMessage(err, msg, args...)
	s.LoadStatus = &runtimev1.LoadStatus{State: runtimev1.LoadStatus_ERROR, Message: message, Failure: operationFailure("runtime.load", err, message)}
	return &runtimev1.LoadResponse{Status: s.LoadStatus}, nil
}

// ── Init ──────────────────────────────────────────────────

func (s *RuntimeWrapper) InitResponse() (*runtimev1.InitResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.InitStatus = &runtimev1.InitStatus{State: runtimev1.InitStatus_READY}
	return &runtimev1.InitResponse{
		Status:                s.InitStatus,
		NetworkMappings:       s.NetworkMappings,
		RuntimeConfigurations: s.RuntimeConfigurations,
	}, nil
}

func (s *RuntimeWrapper) InitError(err error) (*runtimev1.InitResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.InitStatus = &runtimev1.InitStatus{State: runtimev1.InitStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.init", err, err.Error())}
	return &runtimev1.InitResponse{Status: s.InitStatus}, nil
}

func (s *RuntimeWrapper) InitErrorf(err error, msg string, args ...any) (*runtimev1.InitResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := ErrorMessage(err, msg, args...)
	s.InitStatus = &runtimev1.InitStatus{State: runtimev1.InitStatus_ERROR, Message: message, Failure: operationFailure("runtime.init", err, message)}
	return &runtimev1.InitResponse{Status: s.InitStatus}, nil
}

// ── Start ─────────────────────────────────────────────────

func (s *RuntimeWrapper) StartResponse() (*runtimev1.StartResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.generation++
	s.health = nil
	s.StartStatus = &runtimev1.StartStatus{State: runtimev1.StartStatus_STARTED, Generation: s.generation, StartedArtifact: s.startedArtifact}
	// Consumed, like the health report this clears beside it. What was started
	// is a fact about one process, and the next Start is a different one: an
	// agent that does not say what it ran this time must report nothing, not
	// the previous process's answer. The status keeps the pointer, so
	// Information still answers for the process that is running.
	s.startedArtifact = nil
	return &runtimev1.StartResponse{Status: s.StartStatus}, nil
}

// ReportStartedArtifact records what this agent started — the program it ran
// and the files that program was handed — for the StartStatus the next Start
// and every Information after it reports. An agent that calls it implements
// runtime-started-artifact/v1 and may advertise the capability; one that never
// does reports no artifact, and a consumer that needs one refuses by naming it
// rather than deriving a path from the agent's layout.
//
// It applies to ONE start attempt. Call it before every StartResponse that has
// an artifact to name: a process is started, then described, and StartResponse
// consumes what was reported — as StartError discards it, so an artifact named
// for a process that never started is not stamped on the retry that does. An
// agent that reports at its first start and not at its restart reports nothing
// for the restart, which is a refusal downstream rather than the previous
// process's answer approved for this one. Passing nil withdraws a report not
// yet stamped.
func (s *RuntimeWrapper) ReportStartedArtifact(artifact *runtimev1.StartedArtifact) {
	s.Lock()
	defer s.Unlock()
	if artifact == nil {
		s.startedArtifact = nil
		return
	}
	// The caller keeps its message: the artifact it holds must not be mutated
	// underneath it by a later stamp, as ReportHealth's clone exists for.
	s.startedArtifact, _ = proto.Clone(artifact).(*runtimev1.StartedArtifact)
}

// ReportHealth records the agent's latest evaluation of the declared health
// predicates. The report is stamped with the current lifecycle generation, so a
// report produced for a previous process is not mistaken for this one's.
func (s *RuntimeWrapper) ReportHealth(report *basev1.HealthReport) {
	s.Lock()
	defer s.Unlock()
	if report == nil {
		s.health = nil
		return
	}
	// The caller keeps its message: a plugin that re-submits one cached report
	// would otherwise have its Generation rewritten underneath a marshal of the
	// same pointer already handed out by InformationResponse.
	stamped, _ := proto.Clone(report).(*basev1.HealthReport)
	stamped.Generation = s.generation
	s.health = stamped
}

func (s *RuntimeWrapper) StartError(err error) (*runtimev1.StartResponse, error) {
	s.Lock()
	defer s.Unlock()
	// The attempt is over, so its report is too: an artifact named for a
	// process that never started must not be stamped on the retry that does.
	s.startedArtifact = nil
	s.StartStatus = &runtimev1.StartStatus{State: runtimev1.StartStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.start", err, err.Error()), Generation: s.generation}
	return &runtimev1.StartResponse{Status: s.StartStatus}, nil
}

func (s *RuntimeWrapper) StartErrorf(err error, msg string, args ...any) (*runtimev1.StartResponse, error) {
	s.Lock()
	defer s.Unlock()
	// See StartError: a failed attempt's report does not outlive it.
	s.startedArtifact = nil
	message := ErrorMessage(err, msg, args...)
	s.StartStatus = &runtimev1.StartStatus{State: runtimev1.StartStatus_ERROR, Message: message, Failure: operationFailure("runtime.start", err, message), Generation: s.generation}
	return &runtimev1.StartResponse{Status: s.StartStatus}, nil
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
	s.StartStatus = &runtimev1.StartStatus{State: runtimev1.StartStatus_ERROR, Message: msg, Failure: operationFailure("runtime.start", err, msg), Generation: s.generation}
}

// ── Test ──────────────────────────────────────────────────

func (s *RuntimeWrapper) TestResponse() (*runtimev1.TestResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.TestStatus = &runtimev1.TestStatus{State: runtimev1.TestStatus_SUCCESS}
	return &runtimev1.TestResponse{Status: s.TestStatus}, nil
}

func (s *RuntimeWrapper) TestResponseWithResults(run, passed, failed, skipped int32, coverage float32, failures []string, err error) (*runtimev1.TestResponse, error) {
	s.Lock()
	defer s.Unlock()
	if err != nil || failed > 0 {
		msg := ""
		var failure *basev1.Failure
		if err != nil {
			msg = err.Error()
			failure = operationFailure("runtime.test", err, msg)
		} else {
			msg = "one or more tests failed"
			failure = codeflyfailures.New(basev1.FailureCode_FAILURE_CODE_VALIDATION_FAILED, "runtime.test", msg)
		}
		s.TestStatus = &runtimev1.TestStatus{State: runtimev1.TestStatus_ERROR, Message: msg, Failure: failure}
	} else {
		s.TestStatus = &runtimev1.TestStatus{State: runtimev1.TestStatus_SUCCESS}
	}
	return &runtimev1.TestResponse{
		Status:       s.TestStatus,
		TestsRun:     run,
		TestsPassed:  passed,
		TestsFailed:  failed,
		TestsSkipped: skipped,
		CoveragePct:  coverage,
		Failures:     failures,
	}, nil
}

func (s *RuntimeWrapper) TestError(err error) (*runtimev1.TestResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := err.Error()
	s.TestStatus = &runtimev1.TestStatus{State: runtimev1.TestStatus_ERROR, Message: message, Failure: operationFailure("runtime.test", err, message)}
	return typedTestErrorResponse(s.TestStatus, message), nil
}

func (s *RuntimeWrapper) TestErrorf(err error, msg string, args ...any) (*runtimev1.TestResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := ErrorMessage(err, msg, args...)
	s.TestStatus = &runtimev1.TestStatus{State: runtimev1.TestStatus_ERROR, Message: message, Failure: operationFailure("runtime.test", err, message)}
	return typedTestErrorResponse(s.TestStatus, message), nil
}

// typedTestErrorResponse makes pre-execution failures terminal and explicit.
// A status-only response decodes as TestRunResult_UNKNOWN and loses the
// distinction between a failed test and an environment that never ran tests.
func typedTestErrorResponse(status *runtimev1.TestStatus, message string) *runtimev1.TestResponse {
	return &runtimev1.TestResponse{
		Status: status,
		Result: &runtimev1.TestRunResult{State: runtimev1.TestRunResult_ERRORED, Message: message},
		Counts: &runtimev1.TestCounts{},
	}
}

// ── Build ─────────────────────────────────────────────────

func (s *RuntimeWrapper) BuildResponse(output string) (*runtimev1.BuildResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.BuildStatus = &runtimev1.BuildStatus{State: runtimev1.BuildStatus_SUCCESS}
	return &runtimev1.BuildResponse{Status: s.BuildStatus, Output: output}, nil
}

func (s *RuntimeWrapper) BuildError(err error) (*runtimev1.BuildResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.BuildStatus = &runtimev1.BuildStatus{State: runtimev1.BuildStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.compile", err, err.Error())}
	return &runtimev1.BuildResponse{Status: s.BuildStatus}, nil
}

func (s *RuntimeWrapper) BuildErrorf(err error, msg string, args ...any) (*runtimev1.BuildResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := ErrorMessage(err, msg, args...)
	s.BuildStatus = &runtimev1.BuildStatus{State: runtimev1.BuildStatus_ERROR, Message: message, Failure: operationFailure("runtime.compile", err, message)}
	return &runtimev1.BuildResponse{Status: s.BuildStatus}, nil
}

// ── Lint ──────────────────────────────────────────────────

func (s *RuntimeWrapper) LintResponse(output string) (*runtimev1.LintResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.LintStatus = &runtimev1.LintStatus{State: runtimev1.LintStatus_SUCCESS}
	return &runtimev1.LintResponse{Status: s.LintStatus, Output: output}, nil
}

func (s *RuntimeWrapper) LintError(err error) (*runtimev1.LintResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.LintStatus = &runtimev1.LintStatus{State: runtimev1.LintStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.lint", err, err.Error())}
	return &runtimev1.LintResponse{Status: s.LintStatus, Output: err.Error()}, nil
}

func (s *RuntimeWrapper) LintErrorf(err error, msg string, args ...any) (*runtimev1.LintResponse, error) {
	s.Lock()
	defer s.Unlock()
	message := ErrorMessage(err, msg, args...)
	s.LintStatus = &runtimev1.LintStatus{State: runtimev1.LintStatus_ERROR, Message: message, Failure: operationFailure("runtime.lint", err, message)}
	return &runtimev1.LintResponse{Status: s.LintStatus, Output: message}, nil
}

// ── Stop / Destroy ────────────────────────────────────────

func (s *RuntimeWrapper) StopResponse() (*runtimev1.StopResponse, error) {
	return &runtimev1.StopResponse{Status: &runtimev1.StopStatus{State: runtimev1.StopStatus_SUCCESS}}, nil
}

func (s *RuntimeWrapper) StopError(err error) (*runtimev1.StopResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.StopStatus = &runtimev1.StopStatus{State: runtimev1.StopStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.stop", err, err.Error())}
	return &runtimev1.StopResponse{Status: s.StopStatus}, nil
}

func (s *RuntimeWrapper) DestroyResponse() (*runtimev1.DestroyResponse, error) {
	return &runtimev1.DestroyResponse{Status: &runtimev1.DestroyStatus{State: runtimev1.DestroyStatus_SUCCESS}}, nil
}

func (s *RuntimeWrapper) DestroyError(err error) (*runtimev1.DestroyResponse, error) {
	s.Lock()
	defer s.Unlock()
	s.DestroyStatus = &runtimev1.DestroyStatus{State: runtimev1.DestroyStatus_ERROR, Message: err.Error(), Failure: operationFailure("runtime.destroy", err, err.Error())}
	return &runtimev1.DestroyResponse{Status: s.DestroyStatus}, nil
}

// ── Information ───────────────────────────────────────────

func NOOP() *runtimev1.DesiredState {
	return &runtimev1.DesiredState{Stage: runtimev1.DesiredState_NOOP}
}

func (s *RuntimeWrapper) InformationResponse(_ context.Context, _ *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	s.Lock()
	defer s.Unlock()

	if s.DesiredState == nil {
		s.DesiredState = NOOP()
	}
	resp := &runtimev1.InformationResponse{
		LoadStatus:    s.LoadStatus,
		InitStatus:    s.InitStatus,
		StartStatus:   s.StartStatus,
		StopStatus:    s.StopStatus,
		DestroyStatus: s.DestroyStatus,
		TestStatus:    s.TestStatus,
		BuildStatus:   s.BuildStatus,
		LintStatus:    s.LintStatus,
		DesiredState:  s.DesiredState,
		Health:        s.health,
	}
	s.DesiredState = NOOP()
	return resp, nil
}

// ── Desired State ─────────────────────────────────────────

func (s *RuntimeWrapper) DesiredLoad() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev1.DesiredState{Stage: runtimev1.DesiredState_LOAD}
}

func (s *RuntimeWrapper) DesiredInit() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev1.DesiredState{Stage: runtimev1.DesiredState_INIT}
}

func (s *RuntimeWrapper) DesiredStart() {
	s.Lock()
	defer s.Unlock()
	s.DesiredState = &runtimev1.DesiredState{Stage: runtimev1.DesiredState_START}
}

// ── Logging ───────────────────────────────────────────────

func (s *RuntimeWrapper) LogLoadRequest(req *runtimev1.LoadRequest) {
	s.Wool.In("runtime::load").Debug("input",
		wool.Field("environment", req.GetEnvironment()),
		wool.Field("identity", req.GetIdentity()))
}

func (s *RuntimeWrapper) LogInitRequest(req *runtimev1.InitRequest) {
	s.Wool.In("runtime::init").Debug("input",
		wool.Field("runtime-context", req.GetRuntimeContext().GetKind()),
		wool.Field("configurations", resources.MakeConfigurationSummary(req.GetConfiguration())),
		wool.Field("dependencies configurations", resources.MakeManyConfigurationSummary(req.GetDependenciesConfigurations())),
		wool.Field("dependency endpoints", resources.MakeManyEndpointSummary(req.GetDependenciesEndpoints())),
		wool.Field("network mapping", resources.MakeManyNetworkMappingSummary(req.GetProposedNetworkMappings())))
}

func (s *RuntimeWrapper) LogStartRequest(req *runtimev1.StartRequest) {
	s.Wool.In("runtime::start").Debug("input",
		wool.Field("other network mappings", resources.MakeManyNetworkMappingSummary(req.GetDependenciesNetworkMappings())))
}

// ── Runtime Context ───────────────────────────────────────

// The runtime-context accessors are nil-safe: a nil RuntimeContext means the
// host never called SetRuntimeContext (an orchestration ordering bug, not the
// agent's fault), and an agent must never panic on it. Treat nil as Native —
// the safe default for local dev — so CreateRunnerEnvironment falls through to
// the native branch rather than nil-dereferencing.
func (s *RuntimeWrapper) IsContainerRuntime() bool {
	return s.RuntimeContext != nil && s.RuntimeContext.Kind == resources.RuntimeContextContainer
}

func (s *RuntimeWrapper) IsNixRuntime() bool {
	return s.RuntimeContext != nil && s.RuntimeContext.Kind == resources.RuntimeContextNix
}

func (s *RuntimeWrapper) IsNativeRuntime() bool {
	return s.RuntimeContext == nil || s.RuntimeContext.Kind == resources.RuntimeContextNative
}

// NetworkAccess returns the address family appropriate for the runtime selected
// by the orchestrator. Native and Nix processes use host-native addresses;
// container runtimes use container-network addresses.
func (s *RuntimeWrapper) NetworkAccess() *basev1.NetworkAccess {
	return resources.NetworkAccessFromRuntimeContext(s.RuntimeContext)
}

// AddSelfEndpoints publishes the service's own endpoints, as its peers reach
// them under this runtime, as CODEFLY__SELF_ENDPOINT__ carriers. It selects
// from the runtime's NetworkMappings the instance matching NetworkAccess(): a
// native or nix process advertises its host-native address, a container
// process the container-network address. Call it after NetworkMappings is set
// in Init, beside the agent's own AddEndpoints call for its listen address.
func (s *RuntimeWrapper) AddSelfEndpoints(ctx context.Context) error {
	if s == nil || s.Base == nil || s.EnvironmentVariables == nil {
		return errors.New("runtime environment is not initialized")
	}
	return s.EnvironmentVariables.AddSelfEndpoints(ctx, s.NetworkMappings, s.NetworkAccess())
}

func (s *RuntimeWrapper) WithContext(runtimeContext *basev1.RuntimeContext) {
	s.RuntimeContext = runtimeContext
}

func (s *RuntimeWrapper) SetEnvironment(environment *basev1.Environment) {
	s.Environment = environment
	s.EnvironmentVariables.SetEnvironment(environment)
}

// ── Fixture ───────────────────────────────────────────────

// The fixture selection is held by the environment manager rather than by this
// wrapper. Load replaces that manager while the wrapper, built once per agent
// process, survives every invocation the process serves — so a selection
// remembered here would outlive the environment it was meant to stamp.

// SetFixtureFromInit records the invocation's fixture selection, carried by
// InitRequest, and exposes it to the service process as CODEFLY__FIXTURE. Init
// is authoritative and replaces any previous selection, including with none: an
// agent process reused for a second invocation must not serve the first one's
// fixture.
func (s *RuntimeWrapper) SetFixtureFromInit(fixture string) {
	if s == nil || s.Base == nil || s.EnvironmentVariables == nil {
		return
	}
	s.EnvironmentVariables.ResetFixture(fixture)
}

// SetFixtureFromStart records a selection carried by StartRequest, for a host
// that does not populate InitRequest.fixture. It neither clears nor overrides
// what Init recorded, because the Start that reaches a service under test is
// often a policy barrier carrying no selection of its own.
func (s *RuntimeWrapper) SetFixtureFromStart(fixture string) {
	if s == nil || s.Base == nil || s.EnvironmentVariables == nil {
		return
	}
	s.EnvironmentVariables.SetFixture(fixture)
}

// ── Overrides ─────────────────────────────────────────────

// SetOverridesFromInit records the process overrides carried by InitRequest and
// exposes them to the service process. Init is authoritative and replaces any
// previous set, including with none, so an agent process reused for a second
// invocation does not serve the first one's values.
func (s *RuntimeWrapper) SetOverridesFromInit(overrides map[string]string) {
	if s == nil || s.Base == nil || s.EnvironmentVariables == nil {
		return
	}
	s.EnvironmentVariables.ResetOverrides(overrides)
}

// SetOverridesFromStart records overrides carried by StartRequest, for a host
// that does not populate InitRequest.overrides. It neither clears nor overrides
// what Init recorded, because the Start that reaches a service under test is
// often a policy barrier carrying nothing of its own.
func (s *RuntimeWrapper) SetOverridesFromStart(overrides map[string]string) {
	if s == nil || s.Base == nil || s.EnvironmentVariables == nil {
		return
	}
	s.EnvironmentVariables.SetOverrides(overrides)
}
