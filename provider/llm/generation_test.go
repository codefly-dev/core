package llm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/provider/llm"
	"github.com/stretchr/testify/require"
)

type controlledGenerationExecutor struct {
	results       map[llm.Invocation]*llm.GenerationResult
	generateCalls int
	lookupCalls   int
}

type adapterGenerationExecutor struct {
	controlled *controlledGenerationExecutor
}

func (e adapterGenerationExecutor) Generate(ctx context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	return e.controlled.Generate(ctx, request)
}

func (e adapterGenerationExecutor) Lookup(ctx context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	return e.controlled.Lookup(ctx, lookup)
}

func newControlledGenerationExecutor(t *testing.T) *controlledGenerationExecutor {
	t.Helper()
	executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{}}
	for _, fixture := range llm.StructuredGenerationConformanceFixtures() {
		result := fixture.Result
		executor.results[fixture.Request.Invocation] = &result
	}
	return executor
}

func (e *controlledGenerationExecutor) Generate(_ context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	e.generateCalls++
	result, ok := e.results[request.Invocation]
	if !ok {
		return nil, fmt.Errorf("invocation was not installed")
	}
	return result, nil
}

func (e *controlledGenerationExecutor) Lookup(_ context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	e.lookupCalls++
	result, ok := e.results[lookup.Invocation]
	if !ok {
		return nil, fmt.Errorf("invocation was not installed")
	}
	return result, nil
}

func TestStructuredClient_InterchangeableExecutorsUseSameCallerLogic(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	controlled := newControlledGenerationExecutor(t)
	for _, executor := range []llm.GenerationExecutor{controlled, adapterGenerationExecutor{controlled: newControlledGenerationExecutor(t)}} {
		client := llm.NewStructuredClient(executor)
		result, err := client.Generate(context.Background(), &fixture.Request)
		require.NoError(t, err)
		require.Equal(t, fixture.Result.JSON, result.JSON)
	}
}

func TestStructuredClient_ValidatesFixturesAndNullableAbstention(t *testing.T) {
	executor := newControlledGenerationExecutor(t)
	client := llm.NewStructuredClient(executor)
	for _, fixture := range llm.StructuredGenerationConformanceFixtures() {
		result, err := client.Generate(context.Background(), &fixture.Request)
		require.NoError(t, err, fixture.Name)
		require.Equal(t, fixture.Result, *result, fixture.Name)
	}
}

func TestStructuredClient_LookupDoesNotGenerateAndBindsIntent(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	executor := newControlledGenerationExecutor(t)
	client := llm.NewStructuredClient(executor)

	result, err := client.Lookup(context.Background(), &llm.GenerationLookup{Invocation: fixture.Request.Invocation, Receipt: fixture.Result.Receipt, Schema: fixture.Request.Schema})
	require.NoError(t, err)
	require.Equal(t, fixture.Result.JSON, result.JSON)
	require.Equal(t, 0, executor.generateCalls)
	require.Equal(t, 1, executor.lookupCalls)

	changed := fixture.Request.Invocation
	changed.IntentDigest = "sha256:changed-intent"
	_, err = client.Lookup(context.Background(), &llm.GenerationLookup{Invocation: changed, Receipt: fixture.Result.Receipt, Schema: fixture.Request.Schema})
	require.ErrorContains(t, err, "not installed")
	require.Equal(t, 0, executor.generateCalls)
}

func TestStructuredClient_PreservesDistinctOutcomesAndUnknownUsage(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	for _, outcome := range []llm.GenerationOutcome{llm.GenerationRefused, llm.GenerationIncomplete, llm.GenerationCanceled, llm.GenerationUncertain} {
		t.Run(string(outcome), func(t *testing.T) {
			result := fixture.Result
			result.Outcome = outcome
			result.JSON = nil
			result.Usage = nil
			if outcome == llm.GenerationRefused {
				result.Refusal = "cannot comply"
			}
			executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &result}}
			got, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
			require.NoError(t, err)
			require.Equal(t, outcome, got.Outcome)
			require.Nil(t, got.Usage)
		})
	}
}

func TestStructuredClient_RejectsLossySchemasAndInvalidCompletedContent(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	unsupported := fixture.Request
	unsupported.Schema = []byte(`{"type":"object","oneOf":[]}`)
	_, err := llm.NewStructuredClient(newControlledGenerationExecutor(t)).Generate(context.Background(), &unsupported)
	require.ErrorContains(t, err, `keyword "oneOf" is unsupported`)

	invalid := fixture.Result
	invalid.JSON = []byte(`{"kind":"other","source":"fixture"}`)
	executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &invalid}}
	_, err = llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.ErrorContains(t, err, "does not match schema")
}
