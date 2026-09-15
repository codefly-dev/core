package llm

import "encoding/json"

// GenerationConformanceFixture is a portable request/result pair adapters can
// use to verify contract preservation without making a provider call.
type GenerationConformanceFixture struct {
	Name    string
	Request GenerationRequest
	Result  GenerationResult
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
