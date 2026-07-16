package session_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/toolbox/conformance"
	conformancehost "github.com/codefly-dev/core/toolbox/conformance/host"
	"github.com/codefly-dev/core/toolbox/launch"
	"github.com/codefly-dev/core/toolbox/session"
)

type fixtureDecider struct{}

func (fixtureDecider) Evaluate(_ context.Context, request *policy.PDPRequest) policy.PDPDecision {
	if request.Tool == conformance.EffectIncrementTool {
		return policy.PDPDecision{Allow: false, Reason: "fixture policy denies effects"}
	}
	return policy.PDPDecision{Allow: true, Reason: "fixture read grant"}
}

type auditRecorder struct {
	mu     sync.Mutex
	events []session.AuditEvent
	failAt session.AuditPhase
}

func (r *auditRecorder) Record(_ context.Context, event session.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	if event.Phase == r.failAt {
		return errors.New("audit fixture failure")
	}
	return nil
}

func (r *auditRecorder) snapshot() []session.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]session.AuditEvent(nil), r.events...)
}

func installFixture(t *testing.T) (*resources.Toolbox, string) {
	t.Helper()
	manifest, err := resources.LoadToolboxFromDir(context.Background(), "../conformance/testdata")
	require.NoError(t, err)
	codeflyHome := t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, codeflyHome)
	target, err := manifest.Agent.Path(context.Background())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	command := exec.Command("go", "build", "-o", target,
		"github.com/codefly-dev/core/toolbox/conformance/cmd/conformance-toolbox")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "build conformance fixture: %s", output)
	return manifest, codeflyHome
}

func openFixture(t *testing.T, decider policy.Decider, audit session.AuditSink) *session.ToolboxSession {
	return openFixtureWithEnvironment(t, decider, audit, nil)
}

func openFixtureWithEnvironment(
	t *testing.T,
	decider policy.Decider,
	audit session.AuditSink,
	environment []string,
) *session.ToolboxSession {
	t.Helper()
	manifest, _ := installFixture(t)
	opened, err := session.Open(context.Background(), session.Options{
		Manifest: manifest,
		Principal: &policy.Principal{
			ID: "user-1", Kind: policy.KindHuman, OrgID: "org-1", ExpiresAt: time.Now().Add(time.Hour),
		},
		Decider: decider,
		Scope: session.Scope{
			TenantID: "tenant-1", Environment: "test", ReleaseID: "release-1",
		},
		Launch: launch.Options{
			Admission: launch.AdmissionLocal, SkipSandbox: true,
		},
		SessionID:   "session-1",
		Audit:       audit,
		Environment: environment,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	return opened
}

func TestSessionRealProcessDiscoveryCallAuditAndCleanup(t *testing.T) {
	audit := &auditRecorder{}
	opened := openFixture(t, policy.AllowAllPDP{}, audit)
	catalog := opened.Catalog()
	require.Equal(t, conformance.FixtureName, catalog.Identity.Name)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, catalog.Digest)
	require.Contains(t, catalog.ToolNames(), conformance.IdentityTool)
	approved, err := opened.DescribeTool(context.Background(), conformance.IdentityTool)
	require.NoError(t, err)
	require.Equal(t, conformance.IdentityTool, approved.Description.Tool.Name)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, approved.Digest)

	arguments, err := structpb.NewStruct(map[string]any{"subject": "audit-secret-subject"})
	require.NoError(t, err)
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	callContext := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}, TraceFlags: trace.FlagsSampled,
	}))
	result, err := opened.Call(callContext, session.CallRequest{
		Name: conformance.IdentityTool, Arguments: arguments,
		RequestID: "request-1", ObjectiveID: "objective-1", TaskID: "task-1",
	})
	require.NoError(t, err)
	require.Equal(t, conformance.ContractVersion,
		result.Response.Content[0].GetStructured().AsMap()["contract"])
	require.NotEmpty(t, result.AuthorizationID)
	require.Equal(t, approved.Digest, result.CatalogDigest)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, result.RequestDigest)

	events := audit.snapshot()
	require.Equal(t, []session.AuditPhase{
		session.AuditDiscovery, session.AuditDescribe, session.AuditAuthorize, session.AuditInvoke, session.AuditResult,
	}, phases(events))
	require.NotContains(t, fmt.Sprintf("%+v", events), "audit-secret-subject",
		"audit events are digest-only and must not serialize arguments")
	require.NotContains(t, fmt.Sprintf("%+v", events), "tenant-1",
		"tenant authority must not leak into ordinary audit serialization")
	for _, event := range events {
		require.Equal(t, "session-1", event.SessionID)
		require.Equal(t, "user-1", event.PrincipalID)
		require.Equal(t, "org-1", event.OrganizationID)
	}
	for _, event := range events[2:] {
		require.Equal(t, "request-1", event.RequestID)
		require.Equal(t, "objective-1", event.ObjectiveID)
		require.Equal(t, "task-1", event.TaskID)
	}
	require.Equal(t, "request-1", result.RequestID)
	require.Equal(t, "objective-1", result.ObjectiveID)
	require.Equal(t, "task-1", result.TaskID)
	require.Equal(t, "release-1", result.ReleaseID)
	require.Equal(t, traceID.String(), result.TraceID)
	require.Equal(t, session.RetryNever, result.Retry)

	require.NoError(t, opened.Close())
	_, err = opened.Call(context.Background(), session.CallRequest{Name: conformance.IdentityTool})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorClosed, callErr.Code)
	require.NoError(t, opened.Close(), "Close is idempotent")
}

func TestSessionProductionAdmissionUsesRealSandbox(t *testing.T) {
	backend, err := sandbox.New()
	if err != nil || backend.Backend() == sandbox.BackendNative {
		t.Skipf("production session requires an enforcing sandbox: %v", err)
	}
	manifest, _ := installFixture(t)
	opened, err := session.Open(context.Background(), session.Options{
		Manifest: manifest,
		Principal: &policy.Principal{
			ID: "user-prod", Kind: policy.KindHuman, OrgID: "org-prod", ExpiresAt: time.Now().Add(time.Hour),
		},
		Decider: policy.AllowAllPDP{},
		// Zero-value Launch is deliberately promoted to production admission.
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Close()) }()
	result, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)
	require.Equal(t, conformance.ContractVersion,
		result.Response.Content[0].GetStructured().AsMap()["contract"])
}

func TestCLIIndependentExternalHostHarness(t *testing.T) {
	manifest, _ := installFixture(t)
	proof, err := conformancehost.RunIdentityProof(context.Background(), conformancehost.IdentityProofOptions{
		Session: session.Options{
			Manifest: manifest,
			Principal: &policy.Principal{
				ID: "mind-fixture", Kind: policy.KindAgent, OrgID: "org-1",
				AgentID: "codefly.dev/mind-fixture:0.0.1", ExpiresAt: time.Now().Add(time.Hour),
			},
			Decider: policy.AllowAllPDP{},
			Scope: session.Scope{
				TenantID: "tenant-1", Environment: "test", ReleaseID: "release-1",
			},
			Launch:    launch.Options{Admission: launch.AdmissionLocal, SkipSandbox: true},
			SessionID: "mind-session-1",
		},
		RequestID: "mind-request-1", ObjectiveID: "objective-1", TaskID: "task-1",
	})
	require.NoError(t, err)
	require.Equal(t, conformance.ContractVersion, proof.ContractVersion)
	require.Contains(t, proof.ToolNames, conformance.IdentityTool)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, proof.CatalogDigest)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, proof.RequestDigest)
	require.Equal(t, "release-1", proof.ReleaseID)
}

func TestSessionHostPolicyDeniesBeforeObservableEffect(t *testing.T) {
	opened := openFixture(t, fixtureDecider{}, nil)
	_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectIncrementTool})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorPolicyDenied, callErr.Code)

	count, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
	require.NoError(t, err)
	require.Equal(t, float64(0), count.Response.Content[0].GetStructured().AsMap()["count"])
}

func TestSessionNormalizesToolErrorAndValidatesRequest(t *testing.T) {
	opened := openFixture(t, policy.AllowAllPDP{}, nil)
	result, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.DeterministicErrorTool})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorTool, callErr.Code)
	require.Equal(t, session.RetryNever, callErr.Retry)
	require.Equal(t, conformance.DeterministicError, result.Response.Error)

	_, err = opened.Call(context.Background(), session.CallRequest{Name: "fixture.not-approved"})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)

	arguments, err := structpb.NewStruct(map[string]any{"resource": "database:other"})
	require.NoError(t, err)
	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.IdentityTool, Arguments: arguments, Resource: "database:tenant-1",
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)

	unknownArguments, err := structpb.NewStruct(map[string]any{"attacker_field": "ignored-by-old-hosts"})
	require.NoError(t, err)
	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.IdentityTool, Arguments: unknownArguments,
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)
	require.Equal(t, session.RetryNever, callErr.Retry)

	oversizedArguments, err := structpb.NewStruct(map[string]any{
		"subject": strings.Repeat("x", session.DefaultMaxRequestBytes+1),
	})
	require.NoError(t, err)
	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.IdentityTool, Arguments: oversizedArguments,
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)
	require.ErrorContains(t, err, "maximum")

	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.IdentityTool, Roots: []string{"file:///workspace/%2e%2e/secrets"},
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)
	require.ErrorContains(t, err, "traversal")

	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.IdentityTool, RequestID: " request-with-whitespace",
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorValidation, callErr.Code)
}

func TestSessionAuditFailureBeforeInvokePreventsEffect(t *testing.T) {
	audit := &auditRecorder{failAt: session.AuditInvoke}
	opened := openFixture(t, policy.AllowAllPDP{}, audit)
	_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectIncrementTool})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorAudit, callErr.Code)

	audit.failAt = ""
	count, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
	require.NoError(t, err)
	require.Equal(t, float64(0), count.Response.Content[0].GetStructured().AsMap()["count"])
}

func TestSessionDescribeAuditFailureCannotBeBypassedThroughCache(t *testing.T) {
	audit := &auditRecorder{failAt: session.AuditDescribe}
	opened := openFixture(t, policy.AllowAllPDP{}, audit)
	_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectIncrementTool})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorAudit, callErr.Code)

	// A failed describe audit must not populate the session approval cache.
	_, err = opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectIncrementTool})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorAudit, callErr.Code)

	audit.failAt = ""
	count, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
	require.NoError(t, err)
	require.Equal(t, float64(0), count.Response.Content[0].GetStructured().AsMap()["count"])
}

func TestSessionTimeoutAndCancellationAreStableCategories(t *testing.T) {
	audit := &auditRecorder{}
	opened := openFixture(t, policy.AllowAllPDP{}, audit)
	arguments, err := structpb.NewStruct(map[string]any{"duration_ms": 500})
	require.NoError(t, err)
	_, err = opened.Call(context.Background(), session.CallRequest{
		Name: conformance.WaitTool, Arguments: arguments, Timeout: 20 * time.Millisecond,
	})
	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorTimeout, callErr.Code)
	require.Equal(t, session.RetrySafe, callErr.Retry,
		"only the descriptor's closed idempotent classification permits a safe retry")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = opened.Call(canceled, session.CallRequest{
		Name: conformance.WaitTool, Arguments: arguments,
	})
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorCanceled, callErr.Code)
	require.Equal(t, session.RetryNever, callErr.Retry, "caller cancellation is never automatically retried")
	require.Contains(t, phases(audit.snapshot()), session.AuditCancel)
}

func TestSessionCrashMatrix(t *testing.T) {
	openFault := func(t *testing.T, fault string, audit session.AuditSink) *session.ToolboxSession {
		t.Helper()
		return openFixtureWithEnvironment(t, policy.AllowAllPDP{}, audit,
			[]string{conformance.FaultEnvironment + "=" + fault})
	}

	t.Run("before authorization", func(t *testing.T) {
		audit := &auditRecorder{}
		opened := openFault(t, conformance.FaultBeforeAuthorization, audit)
		result, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.CrashTool})
		var callErr *session.CallError
		require.ErrorAs(t, err, &callErr)
		require.Nil(t, result)
		require.Contains(t, []session.ErrorCode{session.ErrorBackendUnavailable, session.ErrorTransport}, callErr.Code)
		require.Equal(t, session.RetryNever, callErr.Retry,
			"the host cannot infer retry safety before it approves the descriptor")
		require.NotContains(t, phases(audit.snapshot()), session.AuditAuthorize)
		require.NotContains(t, phases(audit.snapshot()), session.AuditInvoke)
	})

	t.Run("after authorization", func(t *testing.T) {
		audit := &auditRecorder{}
		opened := openFault(t, conformance.FaultAfterAuthorization, audit)
		result, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.CrashTool})
		var callErr *session.CallError
		require.ErrorAs(t, err, &callErr)
		require.NotNil(t, result)
		require.NotEmpty(t, result.AuthorizationID)
		require.Contains(t, []session.ErrorCode{session.ErrorBackendUnavailable, session.ErrorTransport}, callErr.Code)
		require.Equal(t, session.RetryReconcile, callErr.Retry)
		require.Contains(t, phases(audit.snapshot()), session.AuditAuthorize)
		require.Contains(t, phases(audit.snapshot()), session.AuditInvoke)
	})

	t.Run("during execution", func(t *testing.T) {
		opened := openFault(t, conformance.FaultDuringExecution, nil)
		result, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.CrashTool})
		var callErr *session.CallError
		require.ErrorAs(t, err, &callErr)
		require.NotNil(t, result)
		require.Equal(t, session.ErrorInternal, callErr.Code)
		require.Equal(t, session.RetryReconcile, callErr.Retry)

		count, countErr := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
		require.NoError(t, countErr, "panic recovery must keep the disposable process available")
		require.Equal(t, float64(0), count.Response.Content[0].GetStructured().AsMap()["count"])
	})

	t.Run("after side effect", func(t *testing.T) {
		opened := openFault(t, conformance.FaultAfterSideEffect, nil)
		_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.CrashTool})
		var callErr *session.CallError
		require.ErrorAs(t, err, &callErr)
		require.Equal(t, session.ErrorInternal, callErr.Code)
		require.Equal(t, session.RetryReconcile, callErr.Retry,
			"a failure after a side effect requires reconciliation, never automatic retry")

		count, countErr := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
		require.NoError(t, countErr)
		require.Equal(t, float64(1), count.Response.Content[0].GetStructured().AsMap()["count"],
			"the effect must remain observable after handler panic recovery")
	})

	t.Run("during response serialization", func(t *testing.T) {
		opened := openFault(t, conformance.FaultResponseSerialization, nil)
		_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.CrashTool})
		var callErr *session.CallError
		require.ErrorAs(t, err, &callErr)
		require.Equal(t, session.ErrorInternal, callErr.Code)
		require.Equal(t, session.RetryReconcile, callErr.Retry)

		result, recoveryErr := opened.Call(context.Background(), session.CallRequest{Name: conformance.IdentityTool})
		require.NoError(t, recoveryErr, "response-codec failure must not poison later calls")
		require.Equal(t, conformance.ContractVersion,
			result.Response.Content[0].GetStructured().AsMap()["contract"])
	})

	// A fresh process must recover after the hard-exit cases above.
	recovered := openFixture(t, policy.AllowAllPDP{}, nil)
	result, err := recovered.Call(context.Background(), session.CallRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)
	require.Equal(t, conformance.ContractVersion,
		result.Response.Content[0].GetStructured().AsMap()["contract"])
}

func TestSessionRejectsExpiredPrincipalAndReservedEnvironmentBeforeLaunch(t *testing.T) {
	manifest, _ := installFixture(t)
	base := session.Options{
		Manifest: manifest, Decider: policy.AllowAllPDP{},
		Principal: &policy.Principal{
			ID: "user-1", Kind: policy.KindHuman, ExpiresAt: time.Now().Add(-time.Minute),
		},
		Launch: launch.Options{Admission: launch.AdmissionLocal, SkipSandbox: true},
	}
	_, err := session.Open(context.Background(), base)
	require.ErrorContains(t, err, "expired")

	base.Principal.ExpiresAt = time.Now().Add(time.Hour)
	base.Environment = []string{"CODEFLY_SCOPED_AUTHZ_SECRET=attacker"}
	_, err = session.Open(context.Background(), base)
	require.ErrorContains(t, err, "reserved key")
}

func TestConcurrentSessionsDoNotCrossPrincipalScopeOrTrace(t *testing.T) {
	manifest, _ := installFixture(t)
	open := func(principalID, orgID, tenantID, sessionID string, audit *auditRecorder) *session.ToolboxSession {
		opened, err := session.Open(context.Background(), session.Options{
			Manifest: manifest,
			Principal: &policy.Principal{
				ID: principalID, Kind: policy.KindHuman, OrgID: orgID, ExpiresAt: time.Now().Add(time.Hour),
			},
			Decider: policy.AllowAllPDP{},
			Scope: session.Scope{
				TenantID: tenantID, Environment: "concurrency-test",
			},
			Launch:    launch.Options{Admission: launch.AdmissionLocal, SkipSandbox: true},
			SessionID: sessionID,
			Audit:     audit,
		})
		require.NoError(t, err)
		return opened
	}
	auditOne, auditTwo := &auditRecorder{}, &auditRecorder{}
	one := open("principal-1", "org-1", "tenant-1", "session-1", auditOne)
	two := open("principal-2", "org-2", "tenant-2", "session-2", auditTwo)
	defer func() {
		require.NoError(t, one.Close())
		require.NoError(t, two.Close())
	}()

	type outcome struct {
		sessionID string
		traceID   trace.TraceID
		result    *session.CallResult
		err       error
	}
	outcomes := make(chan outcome, 12)
	var wait sync.WaitGroup
	for _, entry := range []struct {
		opened    *session.ToolboxSession
		sessionID string
		seed      byte
	}{{one, "session-1", 1}, {two, "session-2", 101}} {
		for index := 0; index < 6; index++ {
			wait.Add(1)
			go func(opened *session.ToolboxSession, sessionID string, seed byte, index int) {
				defer wait.Done()
				var traceID trace.TraceID
				for i := range traceID {
					traceID[i] = seed + byte(index+i)
				}
				ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: traceID, SpanID: trace.SpanID{seed, 2, 3, 4, 5, 6, 7, byte(index + 1)},
				}))
				result, err := opened.Call(ctx, session.CallRequest{
					Name: conformance.IdentityTool, RequestID: fmt.Sprintf("%s-request-%d", sessionID, index),
				})
				outcomes <- outcome{sessionID: sessionID, traceID: traceID, result: result, err: err}
			}(entry.opened, entry.sessionID, entry.seed, index)
		}
	}
	wait.Wait()
	close(outcomes)
	authorizations := map[string]struct{}{}
	for outcome := range outcomes {
		require.NoError(t, outcome.err)
		require.Equal(t, outcome.traceID.String(), outcome.result.TraceID)
		require.NotContains(t, authorizations, outcome.result.AuthorizationID,
			"every concurrent invocation must receive an independent one-use authorization")
		authorizations[outcome.result.AuthorizationID] = struct{}{}
	}
	require.Len(t, authorizations, 12)
	for _, check := range []struct {
		audit                         *auditRecorder
		sessionID, principalID, orgID string
	}{{auditOne, "session-1", "principal-1", "org-1"}, {auditTwo, "session-2", "principal-2", "org-2"}} {
		for _, event := range check.audit.snapshot() {
			require.Equal(t, check.sessionID, event.SessionID)
			require.Equal(t, check.principalID, event.PrincipalID)
			require.Equal(t, check.orgID, event.OrganizationID)
		}
	}
}

func phases(events []session.AuditEvent) []session.AuditPhase {
	out := make([]session.AuditPhase, 0, len(events))
	for _, event := range events {
		out = append(out, event.Phase)
	}
	return out
}
