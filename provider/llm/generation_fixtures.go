package llm

import "encoding/json"

// GenerationConformanceFixture is a portable request/result pair adapters can
// use to verify contract preservation without making a provider call.
type GenerationConformanceFixture struct {
	Name    string
	Request GenerationRequest
	Result  GenerationResult
}

// ToolTurnConformanceFixture is a complete proposal, caller execution, and
// continuation sequence with no provider-specific state.
type ToolTurnConformanceFixture struct {
	Name         string
	Request      GenerationRequest
	Proposal     GenerationResult
	Continuation ContinuationRequest
	Result       GenerationResult
}

// StructuredGenerationConformanceFixtures returns fixtures that require
// descriptions, constants, enumerations, and nullable structured output to
// survive adapter installation.
func StructuredGenerationConformanceFixtures() []GenerationConformanceFixture {
	fixtures := []GenerationConformanceFixture{
		{
			Name: "described-enumerated-constant-object",
			Request: GenerationRequest{
				Model:      ModelIdentity{Model: "fixture-model", Profile: "fixture-profile"},
				Messages:   []Message{{Role: "user", Content: "classify this"}},
				MaxTokens:  64,
				Schema:     json.RawMessage(`{"type":"object","description":"A classified finding.","properties":{"kind":{"type":"string","description":"The finding category.","enum":["fact","question"]},"source":{"type":"string","const":"fixture"}},"required":["kind","source"],"additionalProperties":false}`),
				Invocation: Invocation{ID: "fixture-1"},
			},
			Result: GenerationResult{Receipt: "fixture-receipt-1", Outcome: GenerationCompleted, Delivery: GenerationResponseReceived, JSON: json.RawMessage(`{"kind":"fact","source":"fixture"}`)},
		},
		{
			Name: "nullable-abstention",
			Request: GenerationRequest{
				Model:      ModelIdentity{Model: "fixture-model"},
				Messages:   []Message{{Role: "user", Content: "answer only when supported"}},
				MaxTokens:  64,
				Schema:     json.RawMessage(`{"type":["object","null"],"description":"A result or an explicit abstention.","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
				Invocation: Invocation{ID: "fixture-2"},
			},
			Result: GenerationResult{Receipt: "fixture-receipt-2", Outcome: GenerationCompleted, Delivery: GenerationResponseReceived, JSON: json.RawMessage(`null`)},
		},
	}
	for i := range fixtures {
		if err := BindInvocation(&fixtures[i].Request, fixtures[i].Request.Invocation.ID); err != nil {
			panic(err)
		}
		fixtures[i].Result.Invocation = fixtures[i].Request.Invocation
	}
	return fixtures
}

// ToolTurnConformanceFixtures returns portable fixtures for injected executors.
func ToolTurnConformanceFixtures() []*ToolTurnConformanceFixture {
	fixtures := []*ToolTurnConformanceFixture{
		{
			Name: "authorized-tool-result-continuation",
			Request: GenerationRequest{
				Model:     ModelIdentity{Model: "fixture-model", Profile: "fixture-profile"},
				Messages:  []Message{{Role: "user", Content: "find the weather"}},
				MaxTokens: 64,
				Schema:    json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string","description":"The final weather answer."}},"required":["answer"],"additionalProperties":false}`),
				Tools: []ToolDeclaration{{
					Name:        "weather",
					Description: "Read the current weather for one supported city.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","description":"A supported city name.","enum":["Montreal","New York"]},"unit":{"type":"string","const":"celsius"}},"required":["city","unit"],"additionalProperties":false}`),
				}},
				Invocation: Invocation{ID: "tool-fixture-1"},
			},
			Proposal: GenerationResult{
				Receipt:      "tool-fixture-receipt-1",
				Outcome:      GenerationToolCalls,
				Delivery:     GenerationResponseReceived,
				ToolCalls:    []ToolProposal{{ID: "call-weather-1", Name: "weather", Arguments: json.RawMessage(`{"city":"Montreal","unit":"celsius"}`)}},
				Continuation: "opaque-continuation-1",
			},
			Continuation: ContinuationRequest{
				Results: []AuthorizedToolResult{{
					AuthorizationReference: "caller-authorization-1",
					Result: ToolResult{
						CallID:  "call-weather-1",
						Outcome: ToolResultSucceeded,
						JSON:    json.RawMessage(`{"temperature":12,"unit":"celsius"}`),
					},
				}},
				Invocation: Invocation{ID: "tool-fixture-2"},
			},
			Result: GenerationResult{
				Receipt:  "tool-fixture-receipt-2",
				Outcome:  GenerationCompleted,
				Delivery: GenerationResponseReceived,
				JSON:     json.RawMessage(`{"answer":"It is 12 degrees Celsius in Montreal."}`),
			},
		},
	}
	for _, fixture := range fixtures {
		if err := BindInvocation(&fixture.Request, fixture.Request.Invocation.ID); err != nil {
			panic(err)
		}
		fixture.Proposal.Invocation = fixture.Request.Invocation
		fixture.Continuation.Request = &fixture.Request
		fixture.Continuation.Previous = &fixture.Proposal
		if err := BindContinuation(&fixture.Continuation, fixture.Continuation.Invocation.ID); err != nil {
			panic(err)
		}
		fixture.Result.Invocation = fixture.Continuation.Invocation
	}
	return fixtures
}
