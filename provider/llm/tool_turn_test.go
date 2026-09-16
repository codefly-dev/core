package llm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/provider/llm"
	"github.com/stretchr/testify/require"
)

type controlledToolExecutor struct {
	proposal      *llm.GenerationResult
	result        *llm.GenerationResult
	generateCalls int
	continueCalls int
	lookupCalls   int
	continueErr   error
	lastRequest   *llm.GenerationRequest
}

func (e *controlledToolExecutor) Generate(_ context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	e.generateCalls++
	e.lastRequest = request
	return e.proposal, nil
}

func (e *controlledToolExecutor) Continue(context.Context, *llm.ContinuationRequest) (*llm.GenerationResult, error) {
	e.continueCalls++
	if e.continueErr != nil {
		return nil, e.continueErr
	}
	return e.result, nil
}

func (e *controlledToolExecutor) Lookup(_ context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	e.lookupCalls++
	if lookup.Continuation != nil {
		return e.result, nil
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

func (e adapterToolExecutor) Continue(context.Context, *llm.ContinuationRequest) (*llm.GenerationResult, error) {
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

	_, err := llm.NewStructuredClient(executor).Generate(t.Context(), &fixture.Request)
	require.ErrorContains(t, err, "arguments")
	require.ErrorContains(t, err, "does not match schema")
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
}

func TestStructuredClient_RejectsLookupReceiptMismatch(t *testing.T) {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	executor := newToolExecutor()

	_, err := llm.NewStructuredClient(executor).Lookup(t.Context(), &llm.GenerationLookup{
		Continuation: &fixture.Continuation,
		Receipt:      "another-receipt",
	})
	require.ErrorContains(t, err, "receipt does not match")
	require.Zero(t, executor.generateCalls)
	require.Zero(t, executor.continueCalls)
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
	return &controlledToolExecutor{proposal: &fixture.Proposal, result: &fixture.Result}
}

func newAdapterToolExecutor() adapterToolExecutor {
	fixture := llm.ToolTurnConformanceFixtures()[0]
	return adapterToolExecutor{proposal: &fixture.Proposal, result: &fixture.Result}
}
