# LLM contracts

Core exposes two independent LLM surfaces in `provider/llm`:

- `Client` preserves the existing provider-broker chat, streaming chat,
  embedding, and reranking API.
- `StructuredClient` accepts a language-neutral `GenerationExecutor` selected
  by composition. `ToolGenerationExecutor` is the compatible opt-in extension
  for caller-driven tool continuations.

The provider broker's `ExecuteRequestRequest` cannot express schema fidelity,
typed model outcomes, opaque model continuations, or lookup without generation.
Those concepts therefore live in the consumer-facing generation contract.
Provider envelopes and provider SDK types remain inside executor adapters.
Existing `GenerationExecutor` implementations and all broker callers remain
source-compatible; an executor implements `ToolGenerationExecutor` only when it
supports tool turns.

## Compatibility map

| Harness concept | Core contract | Settlement or validation |
| --- | --- | --- |
| Messages and system instruction | `GenerationRequest.Messages`, `System` | Included in the invocation intent digest |
| Tool declaration | `ToolDeclaration` | Name uniqueness and exact input schema are checked before dispatch |
| Tool proposal | `GenerationToolCalls`, `ToolProposal` | Call IDs are nonempty and unique; arguments validate against the declared tool |
| Authorized execution | `AuthorizedToolResult.AuthorizationReference` | A bounded non-secret caller reference is required, excluded from model intent, and never sent to the model executor |
| Committed tool result | `ToolResult` | Success carries typed JSON; failure carries an error, never both |
| Continuation | `ContinuationRequest`, `ToolContinuationRequest`, `GenerationResult.Continuation` | Core validates caller policy metadata, then sends only the opaque reference and committed results to the executor |
| Structured output | `GenerationRequest.Schema`, `GenerationResult.JSON` | Completed nonempty JSON validates against the exact installed schema; JSON `null` can represent schema-permitted abstention |
| Model/profile identity | `ModelIdentity` | Both values are included in intent identity |
| Limits | `MaxTokens`, `Temperature` | Included in intent identity; adapters translate them to provider profiles |
| Refusal and truncation | `GenerationRefused`, `GenerationIncomplete` | Refusal text and partial text are disjoint from completed JSON |
| Cancellation and uncertain delivery | `GenerationCanceled`, `GenerationUncertain`, `GenerationDelivery` | Content outcome remains separate from dispatch settlement |
| Usage | `*GenerationUsage` with pointer counts | `nil` means unknown; a non-nil zero is an observed zero |
| Stable request identity | `Invocation` | SHA-256 intent digest prevents changed requests or tool results from reusing an ID |
| Lookup/recovery | `GenerationExecutor.Lookup` | Lookup accepts an original or continuation request, may return a rotated receipt, and never calls generate |

## Structured schema profile

Schemas and tool input schemas use the same deliberately bounded JSON Schema
profile. Supported keywords are `type`, `description`, `properties`, `required`,
`additionalProperties`, `items`, `enum`, and `const`. Supported types are
`object`, `array`, `string`, `number`, `integer`, `boolean`, and `null`, including
type arrays for nullable values. Unknown keywords, malformed schemas, duplicate
JSON keys, and unsupported constructs are rejected before an executor runs.
Descriptions, constants, and enumerations stay in the request delivered to the
adapter and in its intent digest; conversion must either preserve them or reject
installation.

## Tool-turn lifecycle

`Generate` can finish with validated JSON or return terminal tool proposals plus
an opaque continuation. The consumer authorizes and executes each proposed call,
then supplies exactly one committed result per call to `Continue`. Each
continuation is a separate stable invocation, so changing a result requires a
new identity. Authorization references and recovery receipts are operation
metadata and do not change that model intent. Missing, duplicated, or
mismatched data produces a typed `ContinuationError` before dispatch. Executors
report stale references with `ContinuationExpired` and replay conflicts with
`ContinuationDuplicate`.

Every executor must atomically bind an invocation ID to its first intent digest
before dispatch. The same ID and digest recovers the existing invocation instead
of generating again; the same ID with another digest returns
`ErrInvocationConflict`. A tool executor verifies that each opaque continuation
belongs to the direct predecessor invocation, consumes it for one invocation,
and rejects use by another invocation with `ContinuationDuplicate`. Lookup
observes durable state and propagates lookup cancellation as an operation error;
it never manufactures a generation outcome.

Each API call is unary and bounded to one model turn. A consumer may apply its
own policy and call `Continue` again if another proposal is returned. Core owns
neither that loop nor prompt/context policy. Structured streaming is not part of
this contract; the existing `Client.ChatStream` API is unchanged.

## Ownership and adoption

Core owns these request, result, validation, identity, and conformance-fixture
types. The consuming harness owns prompt/context policy, tool authorization and
execution, and loop bounds. Composition selects the executor. The external
model owner owns credentials, model/profile translation, provider-specific
state, and durable invocation and continuation semantics.

Adapters can install `StructuredGenerationConformanceFixtures` and
`ToolTurnConformanceFixtures` without a provider call. These fixtures verify
contract preservation, not model quality.

Consumers must pin the exact reviewed Core release rather than `main`. Migrate
with:

```sh
go get github.com/codefly-dev/core@v0.3.34
go mod tidy
```

After merge and green Core CI, the repository's version workflow publishes the
immutable [`v0.3.34` tag](https://github.com/codefly-dev/core/tree/v0.3.34).
That tag and its successful Core workflow are the release evidence for
downstream adoption; consumers must not adopt the branch before they exist.
