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
	results map[llm.Invocation]*llm.GenerationResult
}

func (e adapterGenerationExecutor) Generate(_ context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	result, ok := e.results[request.Invocation]
	if !ok {
		return nil, fmt.Errorf("invocation was not installed")
	}
	return result, nil
}

func (e adapterGenerationExecutor) Lookup(_ context.Context, lookup *llm.GenerationLookup) (*llm.GenerationResult, error) {
	result, ok := e.results[lookup.Request.Invocation]
	if !ok {
		return nil, fmt.Errorf("invocation was not installed")
	}
	return result, nil
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
	result, ok := e.results[lookup.Request.Invocation]
	if !ok {
		return nil, fmt.Errorf("invocation was not installed")
	}
	return result, nil
}

func TestStructuredClient_InterchangeableExecutorsUseSameCallerLogic(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	result := fixture.Result
	for _, executor := range []llm.GenerationExecutor{
		newControlledGenerationExecutor(t),
		adapterGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &result}},
	} {
		got, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
		require.NoError(t, err)
		require.Equal(t, fixture.Result.JSON, got.JSON)
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

func TestStructuredClient_RejectsChangedIntentBeforeDispatchAndLookup(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	executor := newControlledGenerationExecutor(t)
	client := llm.NewStructuredClient(executor)
	changed := fixture.Request
	changed.Messages = []llm.Message{{Role: "user", Content: "a different request"}}

	_, err := client.Generate(context.Background(), &changed)
	require.ErrorContains(t, err, "intent digest does not match")
	require.Equal(t, 0, executor.generateCalls)

	_, err = client.Lookup(context.Background(), &llm.GenerationLookup{Request: &changed, Receipt: fixture.Result.Receipt})
	require.ErrorContains(t, err, "intent digest does not match")
	require.Equal(t, 0, executor.lookupCalls)
}

func TestStructuredClient_LookupDoesNotGenerate(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	executor := newControlledGenerationExecutor(t)
	result, err := llm.NewStructuredClient(executor).Lookup(context.Background(), &llm.GenerationLookup{Request: &fixture.Request, Receipt: fixture.Result.Receipt})
	require.NoError(t, err)
	require.Equal(t, fixture.Result.JSON, result.JSON)
	require.Equal(t, 0, executor.generateCalls)
	require.Equal(t, 1, executor.lookupCalls)
}

func TestStructuredClient_RejectsRefusalWithStructuredContent(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	result := fixture.Result
	result.Outcome = llm.GenerationRefused
	result.Refusal = "cannot comply"
	executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &result}}
	_, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.ErrorContains(t, err, "refused generation has invalid settlement or content")
}

func TestStructuredClient_PreservesPartialOutputSeparately(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	result := fixture.Result
	result.Outcome = llm.GenerationIncomplete
	result.JSON = nil
	result.PartialText = `{"kind":"fact","source":"fixture"`
	executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &result}}
	got, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.NoError(t, err)
	require.Equal(t, llm.GenerationIncomplete, got.Outcome)
	require.Equal(t, result.PartialText, got.PartialText)
}

func TestStructuredClient_MapsCanceledExecutorErrorToTypedSettlement(t *testing.T) {
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	executor := generationExecutorFunc(func(context.Context, *llm.GenerationRequest) (*llm.GenerationResult, error) {
		return nil, context.Canceled
	})
	result, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.NoError(t, err)
	require.Equal(t, llm.GenerationCanceled, result.Outcome)
	require.Equal(t, llm.GenerationSentOutcomeUnknown, result.Delivery)
}

func TestStructuredClient_RejectsDuplicateKeysAndInvalidUsage(t *testing.T) {
	require.ErrorContains(t, llm.ValidateStructuredSchema([]byte(`{"type":"object","type":"string"}`)), "duplicate object key")
	fixture := llm.StructuredGenerationConformanceFixtures()[0]
	result := fixture.Result
	result.JSON = []byte(`{"kind":"fact","kind":"question","source":"fixture"}`)
	executor := &controlledGenerationExecutor{results: map[llm.Invocation]*llm.GenerationResult{fixture.Request.Invocation: &result}}
	_, err := llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.ErrorContains(t, err, "duplicate object key")

	negative := int64(-1)
	result = fixture.Result
	result.Usage = &llm.GenerationUsage{InputTokens: &negative}
	executor.results[fixture.Request.Invocation] = &result
	_, err = llm.NewStructuredClient(executor).Generate(context.Background(), &fixture.Request)
	require.ErrorContains(t, err, "usage cannot be negative")
}

type generationExecutorFunc func(context.Context, *llm.GenerationRequest) (*llm.GenerationResult, error)

func (f generationExecutorFunc) Generate(ctx context.Context, request *llm.GenerationRequest) (*llm.GenerationResult, error) {
	return f(ctx, request)
}

func (generationExecutorFunc) Lookup(context.Context, *llm.GenerationLookup) (*llm.GenerationResult, error) {
	return nil, fmt.Errorf("not implemented")
}
