// Package session is the supported host-side Toolbox lifecycle: validate a
// manifest, launch one guarded process, approve its catalog, authorize exact
// requests, invoke them, emit redacted audit events, and clean up.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/codefly-dev/core/agents/manager"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	coretoolbox "github.com/codefly-dev/core/toolbox"
	"github.com/codefly-dev/core/toolbox/launch"
)

const (
	metadataSessionID    = "x-codefly-session-id"
	metadataInvocationID = "x-codefly-invocation-id"
	metadataRequestID    = "x-codefly-request-id"
	metadataObjectiveID  = "x-codefly-objective-id"
	metadataTaskID       = "x-codefly-task-id"
	metadataTraceID      = "x-codefly-trace-id"
	metadataReleaseID    = "x-codefly-release-id"
)

const DefaultMaxRequestBytes = 1 << 20

// ErrorCode is the stable machine-readable category returned by Call.
type ErrorCode string

const (
	ErrorValidation         ErrorCode = "validation"
	ErrorPolicyDenied       ErrorCode = "policy_denied"
	ErrorTimeout            ErrorCode = "timeout"
	ErrorCanceled           ErrorCode = "canceled"
	ErrorBackendUnavailable ErrorCode = "backend_unavailable"
	ErrorTransport          ErrorCode = "transport"
	ErrorTool               ErrorCode = "tool_error"
	ErrorPartialResult      ErrorCode = "partial_result"
	ErrorProtocol           ErrorCode = "protocol_error"
	ErrorInternal           ErrorCode = "internal_failure"
	ErrorAudit              ErrorCode = "audit_error"
	ErrorClosed             ErrorCode = "session_closed"
)

// RetryClass is advice only; ToolboxSession never retries implicitly.
type RetryClass string

const (
	RetryNever     RetryClass = "never"
	RetrySafe      RetryClass = "safe"
	RetryReconcile RetryClass = "reconcile_before_retry"
)

// CallError preserves the machine category and underlying error for errors.Is.
type CallError struct {
	Code  ErrorCode
	Op    string
	Err   error
	Retry RetryClass
}

func (e *CallError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("toolbox session %s: %s: %v", e.Op, e.Code, e.Err)
}

func (e *CallError) Unwrap() error { return e.Err }

// AuditPhase identifies one lifecycle transition. Events contain identities
// and digests, never raw arguments, results, credentials, or authorization
// tokens.
type AuditPhase string

const (
	AuditDiscovery AuditPhase = "discovery"
	AuditDescribe  AuditPhase = "describe"
	AuditAuthorize AuditPhase = "authorize"
	AuditInvoke    AuditPhase = "invoke"
	AuditResult    AuditPhase = "result"
	AuditDeny      AuditPhase = "deny"
	AuditCancel    AuditPhase = "cancellation"
	AuditCleanup   AuditPhase = "cleanup"
)

// AuditEvent is deliberately redacted by construction.
type AuditEvent struct {
	Phase           AuditPhase
	Timestamp       time.Time
	SessionID       string
	RequestID       string
	ObjectiveID     string
	TaskID          string
	TraceID         string
	ReleaseID       string
	InvocationID    string
	Toolbox         string
	Tool            string
	PrincipalID     string
	OrganizationID  string
	CatalogDigest   string
	RequestDigest   string
	AuthorizationID string
	DecisionPath    string
	ErrorCode       ErrorCode
	Duration        time.Duration
	ContentItems    int
	StructuredItems int
	HasToolError    bool
}

// AuditSink receives ordered lifecycle events. An error before invocation is
// fail-closed; an error after invocation is returned with the result preserved.
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

// AuditFunc adapts a function to AuditSink.
type AuditFunc func(context.Context, AuditEvent) error

func (f AuditFunc) Record(ctx context.Context, event AuditEvent) error {
	return f(ctx, event)
}

// Options owns all trusted session composition. Security options are not
// exposed as raw manager.LoadOption values, preventing callers from overriding
// the bound principal, callback PDP, scoped secret, or production admission.
type Options struct {
	Manifest  *resources.Toolbox
	Workspace string
	Principal *policy.Principal
	Decider   policy.Decider
	Scope     Scope

	// Launch controls only the OS admission choice. Zero-value admission is
	// promoted to production. Local tests must explicitly select AdmissionLocal.
	Launch launch.Options

	Environment     []string
	StartupTimeout  time.Duration
	DialTimeout     time.Duration
	LogWriter       io.Writer
	DefaultTTL      time.Duration
	MaxRequestBytes int

	SessionID string
	Audit     AuditSink
}

// Scope is trusted host/session authority, never accepted from tool arguments.
// These values are stamped into the plugin environment and scoped token so the
// verifier can independently reject cross-tenant or cross-environment calls.
type Scope struct {
	TenantID    string
	Environment string
	ReleaseID   string
	ApprovalID  string
}

// CallRequest contains no principal or credential fields; those always come
// from the session. Resource is copied into arguments["resource"] and conflicts
// are rejected so policy and plugin see one identical binding.
type CallRequest struct {
	Name      string
	Arguments *structpb.Struct
	Roots     []string
	Resource  string
	Timeout   time.Duration

	// Correlation is never used as authority. Principal, tenant, environment,
	// and release authority remain session-owned.
	RequestID    string
	ObjectiveID  string
	TaskID       string
	QueryID      string
	ResultBudget *policy.ResultBudget
}

// CallResult carries only safe correlation and protocol output. The scoped
// token and signing secret never leave Session.
type CallResult struct {
	Response        *toolboxv0.CallToolResponse
	InvocationID    string
	AuthorizationID string
	CatalogDigest   string
	RequestDigest   string
	DecisionPath    string
	Retry           RetryClass
	RequestID       string
	ObjectiveID     string
	TaskID          string
	TraceID         string
	ReleaseID       string
}

// ToolboxSession owns one process, catalog snapshot, evaluator, and principal.
type ToolboxSession struct {
	mu              sync.RWMutex
	closed          bool
	plugin          *launch.Plugin
	manifest        *resources.Toolbox
	principal       *policy.Principal
	audience        string
	catalog         *coretoolbox.CatalogSnapshot
	approvals       map[string]*coretoolbox.ApprovedTool
	evaluator       *policy.GatewayEvaluator
	sessionID       string
	audit           AuditSink
	scope           Scope
	maxRequestBytes int
}

// Open validates and launches a complete guarded Toolbox session. Discovery
// and manifest/catalog admission finish before the Session is returned.
func Open(ctx context.Context, options Options) (_ *ToolboxSession, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Manifest == nil {
		return nil, fmt.Errorf("toolbox session open: manifest is required")
	}
	manifest := cloneManifest(options.Manifest)
	if err := manifest.ValidateForProduction(); err != nil {
		return nil, fmt.Errorf("toolbox session open: manifest admission: %w", err)
	}
	principal := clonePrincipal(options.Principal)
	if err := principal.Validate(); err != nil {
		return nil, fmt.Errorf("toolbox session open: %w", err)
	}
	if principal.IsExpired() {
		return nil, fmt.Errorf("toolbox session open: principal credential expired")
	}
	if options.Decider == nil {
		return nil, fmt.Errorf("toolbox session open: decider is required")
	}
	if err := validateEnvironment(options.Environment); err != nil {
		return nil, fmt.Errorf("toolbox session open: environment: %w", err)
	}
	if err := validateScope(options.Scope); err != nil {
		return nil, fmt.Errorf("toolbox session open: scope: %w", err)
	}

	launchOptions := options.Launch
	if launchOptions.Admission == "" {
		launchOptions.Admission = launch.AdmissionProduction
	}
	launchOptions.Workspace = options.Workspace

	secret := policy.NewSpawnSecret()
	ceiling := policy.NewCeilingPDP(options.Decider, manifest.Permissions, true)
	loadOptions := make([]manager.LoadOption, 0, 9)
	if len(options.Environment) > 0 {
		loadOptions = append(loadOptions, manager.WithEnv(options.Environment...))
	}
	// Local compatibility sessions still require bound catalog/request tokens.
	loadOptions = append(loadOptions,
		manager.WithEnv(coretoolbox.RequireBoundAuthorizationEnvironment+"=1"),
		manager.WithUDS(),
	)
	if scopeEnvironment := options.Scope.environment(); len(scopeEnvironment) > 0 {
		// Trusted scope is appended after caller environment. validateEnvironment
		// reserves these keys, so duplicate-key override is impossible.
		loadOptions = append(loadOptions, manager.WithEnv(scopeEnvironment...))
	}
	if options.StartupTimeout > 0 {
		loadOptions = append(loadOptions, manager.WithStartupTimeout(options.StartupTimeout))
	}
	if options.DialTimeout > 0 {
		loadOptions = append(loadOptions, manager.WithDialTimeout(options.DialTimeout))
	}
	if options.LogWriter != nil {
		loadOptions = append(loadOptions, manager.WithLogWriter(options.LogWriter))
	}
	// Security options are last and cannot be replaced through Options.
	loadOptions = append(loadOptions,
		manager.WithPrincipal(principal),
		manager.WithPermissionsCallback(ceiling),
		manager.WithScopedAuthSecret(secret),
	)

	plugin, err := launch.LaunchWithOptions(ctx, manifest, launchOptions, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("toolbox session open: %w", err)
	}
	defer func() {
		if err != nil {
			plugin.Close()
		}
	}()

	catalog, err := discover(ctx, plugin.Client)
	if err != nil {
		return nil, fmt.Errorf("toolbox session open: discovery: %w", err)
	}
	if err := validateRuntimeIdentity(manifest, catalog.Identity); err != nil {
		return nil, fmt.Errorf("toolbox session open: %w", err)
	}
	if err := manifest.ValidateToolCatalog(catalog.ToolNames()...); err != nil {
		return nil, fmt.Errorf("toolbox session open: catalog admission: %w", err)
	}

	audience := manifest.Agent.String()
	defaultTTL := options.DefaultTTL
	if defaultTTL <= 0 {
		defaultTTL = 30 * time.Second
	}
	maxRequestBytes := options.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = DefaultMaxRequestBytes
	}
	s := &ToolboxSession{
		plugin:    plugin,
		manifest:  manifest,
		principal: principal,
		audience:  audience,
		catalog:   catalog,
		approvals: make(map[string]*coretoolbox.ApprovedTool),
		evaluator: &policy.GatewayEvaluator{
			ToolPolicies: map[string]policy.ToolPolicy{
				audience + ":*": policy.ManifestCeilingPolicy{
					Manifest: manifest.Permissions, TTL: defaultTTL, MaxUses: 1,
				},
			},
			Decider: options.Decider, Secret: secret, DefaultTTL: defaultTTL,
		},
		sessionID:       valueOrULID(options.SessionID),
		audit:           options.Audit,
		scope:           options.Scope,
		maxRequestBytes: maxRequestBytes,
	}
	if err := s.record(ctx, AuditEvent{Phase: AuditDiscovery, CatalogDigest: catalog.Digest}); err != nil {
		return nil, fmt.Errorf("toolbox session open: discovery audit: %w", err)
	}
	return s, nil
}

// Catalog returns a deep copy of the approved snapshot.
func (s *ToolboxSession) Catalog() *coretoolbox.CatalogSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog.Clone()
}

// DescribeTool performs phase two of discovery on demand and pins the exact
// descriptor digest for the lifetime of the session. A toolbox that changes a
// selected descriptor after approval is rejected instead of silently widening
// the contract beneath an existing session.
func (s *ToolboxSession) DescribeTool(ctx context.Context, name string) (*coretoolbox.ApprovedTool, error) {
	return s.describeTool(ctx, name, AuditEvent{})
}

func (s *ToolboxSession) describeTool(ctx context.Context, name string, correlation AuditEvent) (*coretoolbox.ApprovedTool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, &CallError{Code: ErrorClosed, Op: "describe", Err: errors.New("session is closed"), Retry: RetryNever}
	}
	if approved := s.approvals[name]; approved != nil {
		clone := cloneApprovedTool(approved)
		s.mu.RUnlock()
		return clone, nil
	}
	if !s.catalog.HasTool(name) {
		s.mu.RUnlock()
		return nil, &CallError{Code: ErrorValidation, Op: "describe", Err: fmt.Errorf("tool %q is not in approved catalog", name), Retry: RetryNever}
	}
	client := s.plugin.Client
	catalog := s.catalog
	s.mu.RUnlock()

	description, err := client.DescribeTool(ctx, &toolboxv0.DescribeToolRequest{Name: name})
	if err != nil {
		return nil, &CallError{Code: classifyTransport(ctx, err), Op: "describe", Err: err, Retry: RetryNever}
	}
	approved, err := catalog.ApproveTool(name, description)
	if err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "describe", Err: err, Retry: RetryNever}
	}
	correlation.Phase = AuditDescribe
	correlation.Tool = name
	correlation.CatalogDigest = approved.Digest
	if err := s.record(ctx, correlation); err != nil {
		return nil, &CallError{Code: ErrorAudit, Op: "describe audit", Err: err, Retry: RetryNever}
	}

	s.mu.Lock()
	if existing := s.approvals[name]; existing != nil {
		if existing.Digest != approved.Digest {
			s.mu.Unlock()
			return nil, &CallError{Code: ErrorValidation, Op: "describe", Err: fmt.Errorf("tool %q descriptor changed during session", name), Retry: RetryNever}
		}
		approved = existing
	} else {
		s.approvals[name] = approved
	}
	s.mu.Unlock()
	return cloneApprovedTool(approved), nil
}

// Call authorizes and invokes one exact request with a single-use scoped token.
func (s *ToolboxSession) Call(ctx context.Context, input CallRequest) (*CallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCorrelation(input); err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "correlation", Err: err, Retry: RetryNever}
	}
	requestID := valueOrULID(input.RequestID)
	traceID := ""
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}
	correlation := AuditEvent{
		RequestID: requestID, ObjectiveID: input.ObjectiveID, TaskID: input.TaskID,
		TraceID: traceID, ReleaseID: s.scope.ReleaseID,
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, &CallError{Code: ErrorClosed, Op: "call", Err: errors.New("session is closed"), Retry: RetryNever}
	}
	client := s.plugin.Client
	s.mu.RUnlock()

	if input.Name == "" || !s.catalog.HasTool(input.Name) {
		return nil, &CallError{Code: ErrorValidation, Op: "call", Err: fmt.Errorf("tool %q is not in approved catalog", input.Name), Retry: RetryNever}
	}
	if err := validateRoots(input.Roots); err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "roots", Err: err, Retry: RetryNever}
	}
	callCtx := ctx
	cancel := func() {}
	if input.Timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, input.Timeout)
	}
	defer cancel()

	approved, err := s.describeTool(callCtx, input.Name, correlation)
	if err != nil {
		return nil, err
	}
	catalogDigest := approved.Digest
	request, resource, err := buildRequest(input)
	if err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "call", Err: err, Retry: RetryNever}
	}
	if size := proto.Size(request); size > s.maxRequestBytes {
		return nil, &CallError{
			Code: ErrorValidation, Op: "call",
			Err: fmt.Errorf("request is %d bytes; maximum is %d", size, s.maxRequestBytes), Retry: RetryNever,
		}
	}
	if err := coretoolbox.ValidateArguments(approved.Description.GetTool().GetInputSchema(), request.GetArguments()); err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "arguments", Err: err, Retry: RetryNever}
	}
	requestDigest, err := coretoolbox.DigestCallToolRequest(request)
	if err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "call", Err: err, Retry: RetryNever}
	}
	invocationID := policy.NewULID()
	started := time.Now()
	caveats, err := (policy.WellKnownCaveats{
		OrganizationID:  s.principal.OrgID,
		TenantID:        s.scope.TenantID,
		Environment:     s.scope.Environment,
		ResourceBinding: resource,
		ReleaseID:       s.scope.ReleaseID,
		ApprovalID:      s.scope.ApprovalID,
		QueryIDs:        queryIDs(input.QueryID),
		ResultBudget:    input.ResultBudget,
	}).Map()
	if err != nil {
		return nil, &CallError{Code: ErrorValidation, Op: "caveats", Err: err, Retry: RetryNever}
	}

	evaluation, err := s.evaluator.EvaluateAndMint(callCtx, policy.EvaluationInput{
		Principal: s.principal, Toolbox: s.audience, Tool: input.Name, Resource: resource,
		CatalogDigest: catalogDigest, RequestDigest: requestDigest, Caveats: caveats,
	})
	if err != nil {
		code := ErrorTransport
		if errors.Is(err, policy.ErrGatewayDeny) {
			code = ErrorPolicyDenied
		}
		denial := correlation
		denial.Phase = AuditDeny
		denial.InvocationID = invocationID
		denial.Tool = input.Name
		denial.CatalogDigest = catalogDigest
		denial.RequestDigest = requestDigest
		denial.ErrorCode = code
		denial.Duration = time.Since(started)
		_ = s.record(callCtx, denial)
		return nil, &CallError{Code: code, Op: "authorize", Err: err, Retry: RetryNever}
	}
	baseEvent := correlation
	baseEvent.InvocationID = invocationID
	baseEvent.Tool = input.Name
	baseEvent.CatalogDigest = catalogDigest
	baseEvent.RequestDigest = requestDigest
	baseEvent.AuthorizationID = evaluation.Authorization.ID
	baseEvent.DecisionPath = evaluation.DecisionPath
	baseEvent.Phase = AuditAuthorize
	if err := s.record(callCtx, baseEvent); err != nil {
		return nil, &CallError{Code: ErrorAudit, Op: "authorize audit", Err: err, Retry: RetryNever}
	}
	baseEvent.Phase = AuditInvoke
	if err := s.record(callCtx, baseEvent); err != nil {
		return nil, &CallError{Code: ErrorAudit, Op: "invoke audit", Err: err, Retry: RetryNever}
	}

	metadataPairs := []string{
		policy.ScopedAuthMetadataKey, evaluation.Token,
		metadataSessionID, s.sessionID,
		metadataInvocationID, invocationID,
		metadataRequestID, requestID,
	}
	metadataPairs = appendMetadata(metadataPairs, metadataObjectiveID, input.ObjectiveID)
	metadataPairs = appendMetadata(metadataPairs, metadataTaskID, input.TaskID)
	metadataPairs = appendMetadata(metadataPairs, metadataTraceID, traceID)
	metadataPairs = appendMetadata(metadataPairs, metadataReleaseID, s.scope.ReleaseID)
	callCtx = metadata.AppendToOutgoingContext(callCtx, metadataPairs...)
	response, callErr := client.CallTool(callCtx, request)
	result := &CallResult{
		Response: response, InvocationID: invocationID,
		AuthorizationID: evaluation.Authorization.ID,
		CatalogDigest:   catalogDigest, RequestDigest: requestDigest,
		DecisionPath: evaluation.DecisionPath, Retry: RetryNever,
		RequestID: requestID, ObjectiveID: input.ObjectiveID, TaskID: input.TaskID,
		TraceID: traceID, ReleaseID: s.scope.ReleaseID,
	}
	if callErr != nil {
		code := classifyTransport(callCtx, callErr)
		retry := retryFor(approved.Idempotency, code)
		result.Retry = retry
		if code == ErrorCanceled || code == ErrorTimeout {
			canceled := baseEvent
			canceled.Phase = AuditCancel
			canceled.ErrorCode = code
			canceled.Duration = time.Since(started)
			_ = s.record(context.Background(), canceled)
		}
		baseEvent.Phase = AuditResult
		baseEvent.ErrorCode = code
		baseEvent.Duration = time.Since(started)
		_ = s.record(context.Background(), baseEvent)
		return result, &CallError{Code: code, Op: "invoke", Err: callErr, Retry: retry}
	}
	if response == nil {
		baseEvent.Phase = AuditResult
		baseEvent.ErrorCode = ErrorProtocol
		baseEvent.Duration = time.Since(started)
		_ = s.record(context.Background(), baseEvent)
		return result, &CallError{Code: ErrorProtocol, Op: "invoke", Err: errors.New("nil CallTool response"), Retry: RetryNever}
	}
	baseEvent.Phase = AuditResult
	baseEvent.Duration = time.Since(started)
	baseEvent.ContentItems, baseEvent.StructuredItems = summarizeContent(response)
	baseEvent.HasToolError = response.GetError() != ""
	if response.GetError() != "" {
		code := ErrorTool
		if len(response.GetContent()) > 0 {
			code = ErrorPartialResult
		}
		baseEvent.ErrorCode = code
		if auditErr := s.record(callCtx, baseEvent); auditErr != nil {
			return result, &CallError{Code: ErrorAudit, Op: "result audit", Err: auditErr, Retry: RetryNever}
		}
		return result, &CallError{Code: code, Op: "invoke", Err: errors.New(response.GetError()), Retry: RetryNever}
	}
	if auditErr := s.record(callCtx, baseEvent); auditErr != nil {
		return result, &CallError{Code: ErrorAudit, Op: "result audit", Err: auditErr, Retry: RetryNever}
	}
	return result, nil
}

// Close terminates the plugin and records cleanup. It is idempotent.
func (s *ToolboxSession) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	plugin := s.plugin
	s.mu.Unlock()
	plugin.Close()
	return s.record(context.Background(), AuditEvent{Phase: AuditCleanup})
}

func (s *ToolboxSession) record(ctx context.Context, event AuditEvent) error {
	if s.audit == nil {
		return nil
	}
	event.Timestamp = time.Now().UTC()
	event.SessionID = s.sessionID
	event.Toolbox = s.audience
	event.PrincipalID = s.principal.ID
	event.OrganizationID = s.principal.OrgID
	if event.ReleaseID == "" {
		event.ReleaseID = s.scope.ReleaseID
	}
	if event.CatalogDigest == "" {
		event.CatalogDigest = s.catalog.Digest
	}
	return s.audit.Record(ctx, event)
}

func discover(ctx context.Context, client toolboxv0.ToolboxClient) (*coretoolbox.CatalogSnapshot, error) {
	identity, err := client.Identity(ctx, &toolboxv0.IdentityRequest{})
	if err != nil {
		return nil, err
	}
	summaries, err := client.ListToolSummaries(ctx, &toolboxv0.ListToolSummariesRequest{})
	if err != nil {
		return nil, err
	}
	return coretoolbox.NewCatalogSnapshot(identity, summaries)
}

func validateRuntimeIdentity(manifest *resources.Toolbox, identity *toolboxv0.IdentityResponse) error {
	if identity.GetName() != manifest.Name || identity.GetVersion() != manifest.Version {
		return fmt.Errorf("runtime identity %s@%s does not match manifest %s",
			identity.GetName(), identity.GetVersion(), manifest.Identity())
	}
	want := append([]string(nil), manifest.CanonicalFor...)
	got := append([]string(nil), identity.GetCanonicalFor()...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return fmt.Errorf("runtime canonical_for %v does not match manifest %v", got, want)
	}
	return nil
}

func buildRequest(input CallRequest) (*toolboxv0.CallToolRequest, string, error) {
	arguments := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if input.Arguments != nil {
		arguments = proto.Clone(input.Arguments).(*structpb.Struct)
		if arguments.Fields == nil {
			arguments.Fields = map[string]*structpb.Value{}
		}
	}
	resource := input.Resource
	if existing, ok := arguments.AsMap()["resource"]; ok {
		existingResource, ok := existing.(string)
		if !ok {
			return nil, "", fmt.Errorf("arguments.resource must be a string")
		}
		if resource != "" && resource != existingResource {
			return nil, "", fmt.Errorf("resource %q conflicts with arguments.resource %q", resource, existingResource)
		}
		resource = existingResource
	}
	if resource != "" {
		value, err := structpb.NewValue(resource)
		if err != nil {
			return nil, "", err
		}
		arguments.Fields["resource"] = value
	}
	if input.QueryID != "" {
		if existing, ok := arguments.AsMap()["query_id"]; ok {
			queryID, ok := existing.(string)
			if !ok || queryID != input.QueryID {
				return nil, "", fmt.Errorf("query_id conflicts with arguments.query_id")
			}
		}
		arguments.Fields["query_id"] = structpb.NewStringValue(input.QueryID)
	}
	if input.ResultBudget != nil {
		if err := input.ResultBudget.Validate(); err != nil {
			return nil, "", err
		}
		if existing, ok := arguments.AsMap()["result_budget"]; ok {
			budget, parseErr := policy.ParseResultBudget(existing)
			if parseErr != nil || budget != *input.ResultBudget {
				return nil, "", fmt.Errorf("result_budget conflicts with arguments.result_budget")
			}
		}
		value, valueErr := structpb.NewValue(map[string]any{
			"max_rows": input.ResultBudget.MaxRows, "max_bytes": input.ResultBudget.MaxBytes,
			"max_duration_ms": input.ResultBudget.MaxDurationMillis,
		})
		if valueErr != nil {
			return nil, "", valueErr
		}
		arguments.Fields["result_budget"] = value
	}
	return &toolboxv0.CallToolRequest{
		Name: input.Name, Arguments: arguments, Roots: append([]string(nil), input.Roots...),
	}, resource, nil
}

func classifyTransport(ctx context.Context, err error) ErrorCode {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return ErrorCanceled
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded:
		return ErrorTimeout
	case codes.Canceled:
		return ErrorCanceled
	case codes.Unavailable:
		return ErrorBackendUnavailable
	case codes.InvalidArgument:
		return ErrorValidation
	case codes.Unauthenticated, codes.PermissionDenied:
		return ErrorPolicyDenied
	case codes.Internal:
		return ErrorInternal
	case codes.DataLoss:
		return ErrorProtocol
	default:
		return ErrorTransport
	}
}

func retryFor(idempotency coretoolbox.IdempotencyClass, code ErrorCode) RetryClass {
	switch code {
	case ErrorTimeout, ErrorBackendUnavailable, ErrorTransport, ErrorInternal, ErrorProtocol:
		switch idempotency {
		case coretoolbox.IdempotencySafe:
			return RetrySafe
		case coretoolbox.IdempotencySideEffecting:
			return RetryReconcile
		}
	}
	return RetryNever
}

func appendMetadata(pairs []string, key, value string) []string {
	if value == "" {
		return pairs
	}
	return append(pairs, key, value)
}

func summarizeContent(response *toolboxv0.CallToolResponse) (contentItems, structuredItems int) {
	if response == nil {
		return 0, 0
	}
	contentItems = len(response.GetContent())
	for _, content := range response.GetContent() {
		if content.GetStructured() != nil {
			structuredItems++
		}
	}
	return contentItems, structuredItems
}

func validateCorrelation(input CallRequest) error {
	for name, value := range map[string]string{
		"request_id": input.RequestID, "objective_id": input.ObjectiveID,
		"task_id": input.TaskID, "query_id": input.QueryID,
	} {
		if strings.TrimSpace(value) != value {
			return fmt.Errorf("%s must not have surrounding whitespace", name)
		}
		if len(value) > 256 {
			return fmt.Errorf("%s exceeds 256 bytes", name)
		}
	}
	return nil
}

func queryIDs(queryID string) []string {
	if queryID == "" {
		return nil
	}
	return []string{queryID}
}

func validateRoots(roots []string) error {
	if len(roots) > 64 {
		return fmt.Errorf("got %d roots; maximum is 64", len(roots))
	}
	seen := make(map[string]struct{}, len(roots))
	for index, root := range roots {
		if root == "" || len(root) > 4096 {
			return fmt.Errorf("root %d must be non-empty and at most 4096 bytes", index)
		}
		if _, duplicate := seen[root]; duplicate {
			return fmt.Errorf("root %d duplicates %q", index, root)
		}
		seen[root] = struct{}{}
		parsed, err := url.Parse(root)
		if err != nil || parsed.Scheme == "" {
			return fmt.Errorf("root %d must be an absolute URI", index)
		}
		if parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("root %d must not contain userinfo or a fragment", index)
		}
		if parsed.Scheme == "file" && !strings.HasPrefix(parsed.Path, "/") {
			return fmt.Errorf("root %d file URI path must be absolute", index)
		}
		for _, segment := range strings.Split(parsed.Path, "/") {
			if segment == ".." || segment == "." {
				return fmt.Errorf("root %d contains traversal segment %q", index, segment)
			}
		}
	}
	return nil
}

func validateEnvironment(environment []string) error {
	reserved := map[string]struct{}{
		"CODEFLY_AGENT_TOKEN": {}, "CODEFLY_PRINCIPAL_TOKEN": {}, "CODEFLY_PRINCIPAL_ID": {},
		"CODEFLY_SCOPED_AUTHZ_SECRET": {}, policy.EnvPermissionsSocket: {}, policy.EnvPDPMode: {},
		coretoolbox.RequireBoundAuthorizationEnvironment: {},
		"CODEFLY_TOOLBOX_NAME":                           {}, "CODEFLY_TOOLBOX_VERSION": {}, "CODEFLY_TOOLBOX_AUDIENCE": {},
		policy.EnvToolboxTenantID: {}, policy.EnvToolboxEnvironment: {},
		policy.EnvToolboxReleaseID: {}, policy.EnvToolboxApprovalID: {},
	}
	for i, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return fmt.Errorf("entry %d must be KEY=VALUE", i)
		}
		if _, blocked := reserved[key]; blocked {
			return fmt.Errorf("entry %d attempts to override reserved key %s", i, key)
		}
	}
	return nil
}

func validateScope(scope Scope) error {
	for name, value := range map[string]string{
		"tenant_id": scope.TenantID, "environment": scope.Environment,
		"release_id": scope.ReleaseID, "approval_id": scope.ApprovalID,
	} {
		if strings.TrimSpace(value) != value {
			return fmt.Errorf("%s must not have surrounding whitespace", name)
		}
	}
	return nil
}

func (s Scope) environment() []string {
	values := make([]string, 0, 4)
	for _, item := range []struct{ key, value string }{
		{policy.EnvToolboxTenantID, s.TenantID},
		{policy.EnvToolboxEnvironment, s.Environment},
		{policy.EnvToolboxReleaseID, s.ReleaseID},
		{policy.EnvToolboxApprovalID, s.ApprovalID},
	} {
		if item.value != "" {
			values = append(values, item.key+"="+item.value)
		}
	}
	return values
}

func clonePrincipal(principal *policy.Principal) *policy.Principal {
	if principal == nil {
		return nil
	}
	clone := *principal
	clone.DelegationChain = append([]policy.DelegationLink(nil), principal.DelegationChain...)
	return &clone
}

func cloneManifest(manifest *resources.Toolbox) *resources.Toolbox {
	clone := *manifest
	if manifest.Agent != nil {
		agent := *manifest.Agent
		clone.Agent = &agent
	}
	clone.CanonicalFor = append([]string(nil), manifest.CanonicalFor...)
	clone.Sandbox.ReadPaths = append([]string(nil), manifest.Sandbox.ReadPaths...)
	clone.Sandbox.WritePaths = append([]string(nil), manifest.Sandbox.WritePaths...)
	clone.Sandbox.UnixSockets = append([]string(nil), manifest.Sandbox.UnixSockets...)
	clone.Permissions.Required = append([]policy.PermissionDeclaration(nil), manifest.Permissions.Required...)
	clone.Permissions.Optional = append([]policy.PermissionDeclaration(nil), manifest.Permissions.Optional...)
	if manifest.Permissions.RiskLevels != nil {
		clone.Permissions.RiskLevels = make(map[string]string, len(manifest.Permissions.RiskLevels))
		for action, level := range manifest.Permissions.RiskLevels {
			clone.Permissions.RiskLevels[action] = level
		}
	}
	return &clone
}

func cloneApprovedTool(approved *coretoolbox.ApprovedTool) *coretoolbox.ApprovedTool {
	if approved == nil {
		return nil
	}
	return &coretoolbox.ApprovedTool{
		Summary:     proto.Clone(approved.Summary).(*toolboxv0.ToolSummary),
		Description: proto.Clone(approved.Description).(*toolboxv0.DescribeToolResponse),
		Digest:      approved.Digest,
		Idempotency: approved.Idempotency,
	}
}

func valueOrULID(value string) string {
	if value != "" {
		return value
	}
	return policy.NewULID()
}
