# Structured generation contract

`provider/llm` exposes a transport-neutral, no-tool and non-streaming structured-generation port through `GenerationExecutor` and `StructuredClient`. It is separate from the existing broker `Executor`: that older interface accepts `ExecuteRequestRequest`, which requires a host-built provider operation, credentials, admission, and response-policy envelope. It cannot express an output schema or recover an invocation without exposing broker protocol types. Existing `Client`, `Executor`, chat, streaming, embedding, rerank, and broker callers remain unchanged.

| Concern | Existing typed/broker API | Structured-generation contract |
| --- | --- | --- |
| Messages | `ChatRequest.Messages` | `GenerationRequest.Messages` and `System` |
| Output schema | Not represented | Exact JSON Schema portable profile in `Schema` |
| Model/profile | `ChatRequest.Model` | `ModelIdentity.Model` and optional adapter-defined `Profile` |
| Limits | `MaxTokens` and temperature | `MaxTokens` and `Temperature` |
| Refusal/truncation | Provider stop text only | `REFUSED`, `INCOMPLETE`, `CANCELED`, and `UNCERTAIN` outcomes plus independent delivery settlement |
| Usage | Integer fields where forwarded | Optional `GenerationUsage`; nil fields are unknown and zero is explicit |
| Stable identity | Broker request/idempotency metadata | `BindInvocation` derives `Invocation.IntentDigest` from the exact request |
| Recovery | Provider checkpoint/receipt mechanisms | `Lookup` with the original request and opaque adapter receipt; it never generates |

The portable schema profile supports `type` (including an array containing `null`), `description`, `properties`, `required`, `additionalProperties`, `items`, `enum`, and `const`. Every other keyword and every duplicate JSON object key is rejected at installation. A completed result carries JSON that validates against the installed schema; JSON `null` is a completed abstention when the schema permits it. Refusal, cancellation, and uncertain outcomes carry no model output. An incomplete result may carry observed `PartialText`, which is never validated or represented as completed structured JSON.

Core owns these types, validators, and portable conformance fixtures from `StructuredGenerationConformanceFixtures`. A consuming harness owns prompt and retry policy. Composition selects a `GenerationExecutor`; its external model module owns credentials, provider/profile translation, dispatch, durable invocation records, and opaque receipt semantics. Callers bind a new request with `BindInvocation`; lookup includes that original request so Core verifies its intent before calling the adapter. The module adapter must bind its durable record to both invocation fields and must not implement `Lookup` by calling `Generate`.

Consumers should depend on `github.com/codefly-dev/core@v0.3.33`, rather than a floating branch or `main` revision. The release tag is published after Core compatibility checks pass; adapters and harnesses should pin that exact tag in their module manifests.
