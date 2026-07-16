// Package conformance provides the canonical Toolbox protocol fixture and the
// reusable host harness that certifies a toolbox implementation. It intentionally
// depends only on core contracts, never on CLI composition.
package conformance

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
)

const (
	FixtureName            = "conformance-fixture"
	FixtureVersion         = "0.0.1"
	ContractVersion        = "codefly.toolbox.conformance/v1"
	IdentityTool           = "fixture.identity.describe"
	DeterministicErrorTool = "fixture.error.deterministic"
	EffectIncrementTool    = "fixture.effect.increment"
	EffectCountTool        = "fixture.effect.count"
	WaitTool               = "fixture.wait"
	CrashTool              = "fixture.process.crash"
	DeterministicError     = "fixture: deterministic error v1"

	// FaultEnvironment selects one deterministic V4 lifecycle fault for the
	// disposable process. It is intentionally fixture-only and never read by
	// production toolboxes.
	FaultEnvironment           = "CODEFLY_CONFORMANCE_FAULT"
	FaultBeforeAuthorization   = "before_authorization"
	FaultAfterAuthorization    = "after_authorization"
	FaultDuringExecution       = "during_execution"
	FaultAfterSideEffect       = "after_side_effect"
	FaultResponseSerialization = "response_serialization"
)

// Server is deterministic except for its deliberately observable in-memory
// effect counter. The counter lets a host prove a denied destructive call never
// reached the handler without touching the filesystem or an external service.
type Server struct {
	*registry.Base
	effectCount atomic.Int64
	crash       func()
	fault       string
}

func New(version string) *Server {
	if version == "" {
		version = FixtureVersion
	}
	s := &Server{crash: func() { os.Exit(86) }, fault: os.Getenv(FaultEnvironment)}
	s.Base = registry.NewBase(registry.Descriptor{
		Name:           FixtureName,
		Version:        version,
		Description:    "Deterministic Codefly Toolbox conformance fixture.",
		SandboxSummary: "no filesystem access; loopback transport only",
	}, s.Tools()...)
	return s
}

// DescribeTool injects the only pre-authorization process failure in the
// fixture. The host has selected a summary but has not yet approved a
// descriptor or minted authority when this process exits.
func (s *Server) DescribeTool(ctx context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	if s.fault == FaultBeforeAuthorization && req.GetName() == CrashTool {
		s.crash()
	}
	return s.Base.DescribeTool(ctx, req)
}

// Tools returns the stable v1 fixture catalog. Renaming or reordering these
// tools is a protocol change and is protected by the JSON/protobuf golden test.
func (s *Server) Tools() []*registry.ToolDefinition {
	emptyInput := respond.Schema(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	})
	return []*registry.ToolDefinition{
		{
			Name:               IdentityTool,
			SummaryDescription: "Return the fixture's deterministic structured identity document.",
			LongDescription: "Returns a stable structured payload used to verify discovery, description, " +
				"schema projection, invocation, and result decoding without external dependencies.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject": map[string]any{
						"type": "string", "default": "codefly", "x-codefly-sensitive": true,
					},
				},
				"additionalProperties": false,
			}),
			OutputSchema: respond.Schema(map[string]any{
				"type":     "object",
				"required": []any{"contract", "fixture", "subject", "capabilities"},
			}),
			Tags:        []string{"read-only", "conformance", "structured"},
			Idempotency: "idempotent",
			ErrorModes:  "Schema-invalid arguments are rejected before dispatch.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Read the default fixture identity.",
				Arguments:       respond.MustStruct(map[string]any{}),
				ExpectedOutcome: "A structured codefly.toolbox.conformance/v1 identity document.",
			}},
			Handler: s.describeIdentity,
		},
		{
			Name:               DeterministicErrorTool,
			SummaryDescription: "Return the fixture's stable in-band error without side effects.",
			LongDescription:    "Always returns the exact deterministic v1 error string for error normalization tests.",
			InputSchema:        emptyInput,
			Tags:               []string{"read-only", "conformance", "error"},
			Idempotency:        "idempotent",
			ErrorModes:         "Always returns `fixture: deterministic error v1` in-band.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Exercise deterministic tool failure.",
				Arguments:       respond.MustStruct(map[string]any{}),
				ExpectedOutcome: DeterministicError,
			}},
			Handler: func(context.Context, *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
				return respond.Error(DeterministicError)
			},
		},
		{
			Name:               EffectIncrementTool,
			SummaryDescription: "Increment the observable fixture counter; destructive and single-use by policy.",
			LongDescription: "Increments an in-memory counter exactly once. Hosts deny this tool to prove policy " +
				"short-circuits before handler dispatch, then read fixture.effect.count as evidence.",
			InputSchema: emptyInput,
			Destructive: true,
			Tags:        []string{"destructive", "conformance", "effect"},
			Idempotency: "side_effecting",
			ErrorModes:  "Policy denial must prevent dispatch; successful dispatch increments exactly once.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Increment the fixture effect counter once.",
				Arguments:       respond.MustStruct(map[string]any{}),
				ExpectedOutcome: "{ count: 1 } for a fresh fixture process.",
			}},
			Handler: s.incrementEffect,
		},
		{
			Name:               EffectCountTool,
			SummaryDescription: "Read the observable fixture effect counter without mutating it.",
			LongDescription:    "Returns the current in-memory effect count as denial and replay evidence.",
			InputSchema:        emptyInput,
			Tags:               []string{"read-only", "conformance", "effect"},
			Idempotency:        "idempotent",
			ErrorModes:         "No tool-level failure modes.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Read the current count.",
				Arguments:       respond.MustStruct(map[string]any{}),
				ExpectedOutcome: "{ count: 0 } before any authorized effect.",
			}},
			Handler: s.readEffectCount,
		},
		{
			Name:               WaitTool,
			SummaryDescription: "Wait for a bounded duration while honoring cancellation and deadlines.",
			LongDescription:    "Waits for duration_ms or returns when the request context is canceled. Used for deterministic timeout and cancellation conformance.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"duration_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 5000},
				},
				"required":             []any{"duration_ms"},
				"additionalProperties": false,
			}),
			Tags:        []string{"read-only", "conformance", "timing"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns a cancellation error when the request context ends before the timer.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Wait for ten milliseconds.",
				Arguments:       respond.MustStruct(map[string]any{"duration_ms": 10}),
				ExpectedOutcome: "{ waited_ms: 10 } unless canceled.",
			}},
			Handler: s.wait,
		},
		{
			Name:               CrashTool,
			SummaryDescription: "Terminate the fixture process with exit code 86 for recovery tests.",
			LongDescription:    "Deliberately terminates the fixture process during execution. Test-only destructive operation for transport and cleanup recovery.",
			InputSchema:        emptyInput,
			Destructive:        true,
			Tags:               []string{"destructive", "conformance", "crash"},
			Idempotency:        "side_effecting",
			ErrorModes:         "The process exits without a tool response; the host observes transport unavailability.",
			Examples: []*toolboxv0.ToolExample{{
				Description:     "Crash the disposable fixture process.",
				Arguments:       respond.MustStruct(map[string]any{}),
				ExpectedOutcome: "The RPC loses its transport and the process exits with code 86.",
			}},
			Handler: s.crashProcess,
		},
	}
}

func (s *Server) describeIdentity(_ context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	subject := "codefly"
	if value, ok := respond.Args(req)["subject"].(string); ok && value != "" {
		subject = value
	}
	identity, _ := s.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	return respond.Struct(map[string]any{
		"contract": ContractVersion,
		"fixture": map[string]any{
			"name":    identity.Name,
			"version": identity.Version,
		},
		"subject": subject,
		"capabilities": []any{
			"discovery", "structured-results", "deterministic-errors", "policy-effects",
		},
	})
}

func (s *Server) incrementEffect(context.Context, *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	return respond.Struct(map[string]any{"count": s.effectCount.Add(1)})
}

func (s *Server) readEffectCount(context.Context, *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	return respond.Struct(map[string]any{"count": s.effectCount.Load()})
}

func (s *Server) wait(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	durationMillis, _ := respond.Args(req)["duration_ms"].(float64)
	timer := time.NewTimer(time.Duration(durationMillis) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return respond.Error("fixture: wait canceled: %v", ctx.Err())
	case <-timer.C:
		return respond.Struct(map[string]any{"waited_ms": int64(durationMillis)})
	}
}

func (s *Server) crashProcess(context.Context, *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	switch s.fault {
	case FaultDuringExecution:
		time.Sleep(20 * time.Millisecond)
		panic("fixture: panic during execution")
	case FaultAfterSideEffect:
		s.effectCount.Add(1)
		panic("fixture: panic after side effect")
	case FaultResponseSerialization:
		// Protobuf strings must be UTF-8. Returning an invalid value forces
		// the gRPC codec's response-marshal path to fail after the handler.
		return respond.Text(string([]byte{0xff}))
	case FaultAfterAuthorization, "":
		s.crash()
	default:
		panic("fixture: unknown fault phase " + s.fault)
	}
	return respond.Error("fixture: crash function unexpectedly returned")
}
