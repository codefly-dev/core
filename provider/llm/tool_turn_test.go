package llm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/provider/llm"
	"github.com/stretchr/testify/require"
)

type controlledToolExecutor struct {
	proposal           *llm.GenerationResult
	result             *llm.GenerationResult
	generateCalls      int
	continueCalls      int
	lookupCalls        int
	continueErr        error
	loseContinueReply  bool
	lookupErr          error
	lastRequest        *llm.GenerationRequest
	lastContinuation   *llm.ToolContinuationRequest
	lastLookup         *llm.GenerationLookup
	mutateRequest      func(*llm.GenerationRequest)
	mutateContinuation func(*llm.ToolContinuationRequest, *llm.GenerationResult)
	invocations        map[string]llm.Invocation
	continuationOwners map[string]llm.Invocation
	continuations      map[string]string
	continuationCache  map[string]*llm.GenerationResult
}

func (e *controlledToolExecutor) Generate(_ context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	e.generateCalls++
	e.lastRequest = request
	if e.mutateRequest != nil {
		e.mutateRequest(request)
	}
	if e.proposal != nil && e.proposal.Continuation != "" {
		e.continuationOwners[e.proposal.Continuation] = e.proposal.Invocation
	}
	return e.proposal, nil
}

func (e *controlledToolExecutor) Continue(_ context.Context, request *llm.ToolContinuationRequest) (*llm.GenerationResult, error) {
	if e.continueErr != nil {
		return nil, e.continueErr
	}
	if e.mutateContinuation != nil {
		e.mutateContinuation(request, e.result)
	}
	if invocation, exists := e.invocations[request.Invocation.ID]; exists {
		if invocation.IntentDigest != request.Invocation.IntentDigest {
			return nil, llm.ErrInvocationConflict
		}
		return e.continuationCache[request.Invocation.ID], nil
	}
	owner, exists := e.continuationOwners[request.Continuation]
	if !exists || owner != request.Previous {
		return nil, &llm.ContinuationError{Code: llm.ContinuationMismatched, Message: "continuation does not belong to previous invocation"}
	}
	if _, consumed := e.continuations[request.Continuation]; consumed {
		return nil, &llm.ContinuationError{Code: llm.ContinuationDuplicate, Message: "continuation already consumed"}
	}
	e.invocations[request.Invocation.ID] = request.Invocation
	e.continuations[request.Continuation] = request.Invocation.ID
	e.continuationCache[request.Invocation.ID] = e.result
	e.continueCalls++
	e.lastContinuation = request
	if e.result != nil && e.result.Continuation != "" {
		e.continuationOwners[e.result.Continuation] = e.result.Invocation
	}
	if e.loseContinueReply {
		e.loseContinueReply = false
		return nil, context.DeadlineExceeded
	}
	return e.result, nil
}

func (e *controlledToolExecutor) Lookup(_ context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	e.lookupCalls++
	e.lastLookup = lookup
	if e.lookupErr != nil {
		return nil, e.lookupErr
	}
	if lookup.Continuation != nil {
		return e.continuationCache[lookup.Continuation.Invocation.ID], nil
	}
	return e.proposal, nil
}

type adapterToolExecutor struct {
	proposal *llm.GenerationResult
	result   *llm.GenerationResult
}

func (e adapterToolExecutor) Generate(context.Context, *llm.GenerationRequest) (*llm.GenerationResult, error) {
	return e.proposal, nil
}

func (e adapterToolExecutor) Continue(context.Context, *llm.ToolContinuationRequest) (*llm.GenerationResult, error) {
	return e.result, nil
}

func (e adapterToolExecutor) Lookup(_ context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	if lookup.Continuation != nil {
		return e.result, nil
	}
	return e.proposal, nil
}

func TestStructuredClient_ToolTurnUsesInterchangeableExecutors(t *testing.T) {
	for _, executor := range []llm.GenerationExecutor{
		newToolExecutor(),
		newAdapterToolExecutor(),
	} {
		fixture := llm.ToolTurnConformanceFixtures()[0]
		client := llm.NewStructuredClient(executor)
		proposal, err := client.Generate(t.Context(), &fixture.Request)
		require.NoError(t, err)
		require.Equal(t, "call-weather-1", proposal.ToolCalls[0].ID)

		fixture.Continuation.Previous = proposal
		require.NoError(t, llm.BindContinuation(&fixture.Continuation, fixture.Continuation.Invocation.ID))
		result, err := client.Continue(t.Context(), &fixture.Continuation)
		require.NoError(t, err)
		require.Equal(t, fixture.Result.JSON, result.JSON)
	}
}

func TestStructuredClient_ValidatesToolArgumentsAgainstInstalledSchema(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	proposal := fixture.Proposal
	proposal.ToolCalls = []llm.ToolProposal{{ID: "call-weather-1", Name: "weather", Arguments: []byte(`{"city":"Paris","unit":"fahrenheit"}`)}}
	executor := newToolExecutor()
	executor.proposal = &proposal
	originalSchema := append([]byte(nil), fixture.Request.Tools[0].InputSchema...)
	executor.mutateRequest = func(request *llm.GenerationRequest) {
		request.Tools[0].InputSchema = []byte(`{"type":"object"}`)
	}

	_, err := llm.NewStructuredClient(executor).Generate(t.Context(), &fixture.Request)
	require.ErrorContains(t, err, "arguments")
	require.ErrorContains(t, err, "does not match schema")
	require.Equal(t, originalSchema, []byte(fixture.Request.Tools[0].InputSchema))
}

func TestStructuredClient_PreservesToolDeclarationSemanticsAtExecutorBoundary(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()

	_, err := llm.NewStructuredClient(executor).Generate(t.Context(), &fixture.Request)
	require.NoError(t, err)
	require.Equal(t, fixture.Request.Tools, executor.lastRequest.Tools)
	require.Contains(t, string(executor.lastRequest.Tools[0].InputSchema), `"description"`)
	require.Contains(t, string(executor.lastRequest.Tools[0].InputSchema), `"enum"`)
	require.Contains(t, string(executor.lastRequest.Tools[0].InputSchema), `"const"`)
}

func TestStructuredClient_StripsAuthorizationFromModelContinuation(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()

	_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, fixture.Proposal.ToolCalls, executor.lastContinuation.ToolCalls)
	require.Equal(t, fixture.Continuation.Results[0].Result, executor.lastContinuation.Results[0])
	require.Equal(t, fixture.Continuation.Previous.Invocation, executor.lastContinuation.Previous)
}

func TestStructuredClient_FreezesContinuationInvocationAcrossExecutor(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	result := *executor.result
	executor.result = &result
	executor.mutateContinuation = func(request *llm.ToolContinuationRequest, result *llm.GenerationResult) {
		request.Invocation = llm.Invocation{ID: "forged", IntentDigest: "forged"}
		result.Invocation = request.Invocation
	}

	_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	require.ErrorContains(t, err, "result invocation does not match")
	require.Equal(t, "tool-fixture-2", fixture.Continuation.Invocation.ID)
}

func TestToolTurnConformanceFixtureOwnsItsContinuationGraph(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	fixture.Request.Model.Model = "installed-model"
	require.NoError(t, llm.BindInvocation(&fixture.Request, fixture.Request.Invocation.ID))
	fixture.Proposal.Invocation = fixture.Request.Invocation
	require.NoError(t, llm.BindContinuation(&fixture.Continuation, fixture.Continuation.Invocation.ID))

	require.Same(t, &fixture.Request, fixture.Continuation.Request)
	require.Same(t, &fixture.Proposal, fixture.Continuation.Previous)
	require.Equal(t, "installed-model", fixture.Continuation.Request.Model.Model)
}

func TestStructuredClient_RejectsInvalidToolResultsBeforeContinuation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*llm.ContinuationRequest)
		code   llm.ContinuationErrorCode
	}{
		{
			name: "missing",
			mutate: func(request *llm.ContinuationRequest) {
				request.Results = nil
			},
			code: llm.ContinuationMissing,
		},
		{
			name: "duplicate",
			mutate: func(request *llm.ContinuationRequest) {
				request.Results = append(request.Results, request.Results[0])
			},
			code: llm.ContinuationDuplicate,
		},
		{
			name: "mismatched",
			mutate: func(request *llm.ContinuationRequest) {
				request.Results[0].Result.CallID = "another-call"
			},
			code: llm.ContinuationMismatched,
		},
		{
			name: "invalid authorization reference",
			mutate: func(request *llm.ContinuationRequest) {
				request.Results[0].AuthorizationReference = string(make([]byte, 257))
			},
			code: llm.ContinuationMismatched,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := llm.ToolTurnConformanceFixtures()[0]
			test.mutate(&fixture.Continuation)
			executor := newToolExecutor()
			_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
			var continuationErr *llm.ContinuationError
			require.ErrorAs(t, err, &continuationErr)
			require.Equal(t, test.code, continuationErr.Code)
			require.Zero(t, executor.continueCalls)
		})
	}
}

func TestStructuredClient_BindsToolResultsToContinuationIntent(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	fixture.Continuation.Results[0].Result.JSON = []byte(`{"temperature":18,"unit":"celsius"}`)
	executor := newToolExecutor()

	_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	require.ErrorContains(t, err, "intent digest does not match")
	require.Zero(t, executor.continueCalls)
}

func TestStructuredClient_ContinuationIdentityAndReferenceAreSingleUse(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	client := llm.NewStructuredClient(executor)

	first, err := client.Continue(t.Context(), &fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, fixture.Result.JSON, first.JSON)

	replayed, err := client.Continue(t.Context(), &fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, fixture.Result.JSON, replayed.JSON)
	require.Equal(t, 1, executor.continueCalls)

	fixture.Continuation.Results[0].Result.JSON = []byte(`{"temperature":18,"unit":"celsius"}`)
	require.NoError(t, llm.BindContinuation(&fixture.Continuation, "tool-fixture-2"))
	_, err = client.Continue(t.Context(), &fixture.Continuation)
	require.ErrorIs(t, err, llm.ErrInvocationConflict)
	require.Equal(t, 1, executor.continueCalls)

	fixture = llm.ToolTurnConformanceFixtures()[0]
	require.NoError(t, llm.BindContinuation(&fixture.Continuation, "tool-fixture-3"))
	_, err = client.Continue(t.Context(), &fixture.Continuation)
	var continuationErr *llm.ContinuationError
	require.ErrorAs(t, err, &continuationErr)
	require.Equal(t, llm.ContinuationDuplicate, continuationErr.Code)
	require.Equal(t, 1, executor.continueCalls)
}

func TestStructuredClient_MultipleToolTurnsPreserveLineageAndRecoverLostReply(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	client := llm.NewStructuredClient(executor)

	firstProposal, err := client.Generate(t.Context(), &fixture.Request)
	require.NoError(t, err)
	fixture.Continuation.Previous = firstProposal
	require.NoError(t, llm.BindContinuation(&fixture.Continuation, fixture.Continuation.Invocation.ID))

	secondProposal := &llm.GenerationResult{
		Invocation:   fixture.Continuation.Invocation,
		Receipt:      "tool-fixture-receipt-2",
		Outcome:      llm.GenerationToolCalls,
		Delivery:     llm.GenerationResponseReceived,
		ToolCalls:    []llm.ToolProposal{{ID: "call-weather-2", Name: "weather", Arguments: []byte(`{"city":"New York","unit":"celsius"}`)}},
		Continuation: "opaque-continuation-2",
	}
	executor.result = secondProposal
	continued, err := client.Continue(t.Context(), &fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, fixture.Continuation.Invocation, continued.Invocation)

	secondContinuation := llm.ContinuationRequest{
		Request:  &fixture.Request,
		Previous: continued,
		Results: []llm.AuthorizedToolResult{{
			AuthorizationReference: "caller-authorization-2",
			Result: llm.ToolResult{
				CallID:  "call-weather-2",
				Outcome: llm.ToolResultSucceeded,
				JSON:    []byte(`{"temperature":20,"unit":"celsius"}`),
			},
		}},
	}
	require.NoError(t, llm.BindContinuation(&secondContinuation, "tool-fixture-3"))
	final := &llm.GenerationResult{
		Invocation: secondContinuation.Invocation,
		Receipt:    "tool-fixture-receipt-3",
		Outcome:    llm.GenerationCompleted,
		Delivery:   llm.GenerationResponseReceived,
		JSON:       []byte(`{"answer":"It is 20 degrees Celsius in New York."}`),
	}
	executor.result = final
	executor.loseContinueReply = true

	uncertain, err := client.Continue(t.Context(), &secondContinuation)
	require.NoError(t, err)
	require.Equal(t, llm.GenerationCanceled, uncertain.Outcome)
	require.Equal(t, 2, executor.continueCalls)

	recovered, err := client.Lookup(t.Context(), &llm.GenerationLookup{
		Continuation: &secondContinuation,
		Receipt:      final.Receipt,
	})
	require.NoError(t, err)
	require.Equal(t, final.JSON, recovered.JSON)
	require.Equal(t, 2, executor.continueCalls)
	require.Equal(t, 1, executor.lookupCalls)
}

func TestStructuredClient_RejectsForeignContinuationPredecessor(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	client := llm.NewStructuredClient(executor)

	proposal, err := client.Generate(t.Context(), &fixture.Request)
	require.NoError(t, err)
	foreign := *proposal
	foreign.Invocation = llm.Invocation{ID: "foreign-turn", IntentDigest: "sha256:foreign"}
	fixture.Continuation.Previous = &foreign
	require.NoError(t, llm.BindContinuation(&fixture.Continuation, fixture.Continuation.Invocation.ID))

	_, err = client.Continue(t.Context(), &fixture.Continuation)
	var continuationErr *llm.ContinuationError
	require.ErrorAs(t, err, &continuationErr)
	require.Equal(t, llm.ContinuationMismatched, continuationErr.Code)
	require.Zero(t, executor.continueCalls)
}

func TestContinuationIntentExcludesAuthorizationAndRecoveryReceipt(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	original, err := llm.ContinuationIntentDigest(&fixture.Continuation)
	require.NoError(t, err)

	fixture.Continuation.Results[0].AuthorizationReference = "renewed-authorization"
	fixture.Proposal.Receipt = "rotated-recovery-receipt"
	changed, err := llm.ContinuationIntentDigest(&fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, original, changed)
}

func TestStructuredClient_PreservesTypedExpiredContinuationError(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	executor.continueErr = &llm.ContinuationError{Code: llm.ContinuationExpired, Message: "reference expired"}

	_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	var continuationErr *llm.ContinuationError
	require.ErrorAs(t, err, &continuationErr)
	require.Equal(t, llm.ContinuationExpired, continuationErr.Code)
}

func TestStructuredClient_ContinuationLookupDoesNotGenerateOrContinue(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()

	result, err := llm.NewStructuredClient(executor).Lookup(t.Context(), &llm.GenerationLookup{
		Continuation: &fixture.Continuation,
		Receipt:      fixture.Result.Receipt,
	})
	require.NoError(t, err)
	require.Equal(t, fixture.Result.JSON, result.JSON)
	require.Zero(t, executor.generateCalls)
	require.Zero(t, executor.continueCalls)
	require.Equal(t, 1, executor.lookupCalls)
	require.Empty(t, executor.lastLookup.Continuation.Results[0].AuthorizationReference)
}

func TestStructuredClient_PreservesRotatedLookupReceipt(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()

	result, err := llm.NewStructuredClient(executor).Lookup(t.Context(), &llm.GenerationLookup{
		Continuation: &fixture.Continuation,
		Receipt:      "previous-receipt",
	})
	require.NoError(t, err)
	require.Equal(t, fixture.Result.Receipt, result.Receipt)
	require.Zero(t, executor.generateCalls)
	require.Zero(t, executor.continueCalls)
}

func TestStructuredClient_LookupCancellationRemainsAnOperationError(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	executor.lookupErr = context.DeadlineExceeded

	result, err := llm.NewStructuredClient(executor).Lookup(t.Context(), &llm.GenerationLookup{
		Continuation: &fixture.Continuation,
		Receipt:      fixture.Result.Receipt,
	})
	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStructuredClient_NoToolExecutorRejectsContinuation(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := generationExecutorFunc(func(context.Context, *llm.GenerationRequest) (*llm.GenerationResult, error) {
		return nil, fmt.Errorf("unexpected generate")
	})

	_, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	var continuationErr *llm.ContinuationError
	require.ErrorAs(t, err, &continuationErr)
	require.Equal(t, llm.ContinuationUnsupported, continuationErr.Code)
}

func TestStructuredClient_MapsCanceledContinuationToTypedSettlement(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()
	executor.continueErr = context.DeadlineExceeded

	result, err := llm.NewStructuredClient(executor).Continue(t.Context(), &fixture.Continuation)
	require.NoError(t, err)
	require.Equal(t, llm.GenerationCanceled, result.Outcome)
	require.Equal(t, llm.GenerationSentOutcomeUnknown, result.Delivery)
}

func newToolExecutor() *controlledToolExecutor {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	return &controlledToolExecutor{
		proposal:           &fixture.Proposal,
		result:             &fixture.Result,
		invocations:        map[string]llm.Invocation{},
		continuationOwners: map[string]llm.Invocation{fixture.Proposal.Continuation: fixture.Proposal.Invocation},
		continuations:      map[string]string{},
		continuationCache:  map[string]*llm.GenerationResult{fixture.Continuation.Invocation.ID: &fixture.Result},
	}
}

func newAdapterToolExecutor() adapterToolExecutor {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	return adapterToolExecutor{proposal: &fixture.Proposal, result: &fixture.Result}
}
