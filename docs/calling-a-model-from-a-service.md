# Calling a model from a service

A codefly service that needs chat, embeddings, or rerank does **not** wire a
vendor SDK and read an API key directly. It calls the model through the provider
broker, so the call inherits admission, SSRF hardening, the credential vault,
response-policy secret filtering, budgets, and deterministic cassettes. The typed
client for this lives in `provider/llm`; the host (the CLI / sdk-go) owns the
broker session, the admitted origin, the credential handle, operation identity,
idempotency, and budget.

This page covers the three things a service author touches: declaring the
dependency, naming the secret, and testing with the recorder.

## Vendors and descriptors

`llm.Manifest(origin, vendor)` builds the provider manifest for one vendor. Each
vendor advertises only the descriptors it actually serves:

| Vendor              | chat                    | embed                       | rerank        |
| ------------------- | ----------------------- | --------------------------- | ------------- |
| `llm.VendorAnthropic` | `/v1/messages`        | —                           | —             |
| `llm.VendorVoyage`    | —                     | `/v1/embeddings`            | `/v1/rerank`  |
| `llm.VendorOpenAI`    | `/v1/chat/completions`| `/v1/embeddings`            | —             |

Anthropic has no embeddings endpoint, so its manifest packages chat only. Its
embeddings partner is Voyage, which also serves rerank. OpenAI serves chat and
embed.

The typed requests carry the vendor-specific fields:

- `EmbedRequest.InputType` — Voyage's retrieval hint (`"query"` or `"document"`).
- `EmbedRequest.Dimensions` — a truncated output width. `PlannedEmbed` renders it
  under whichever field name the target descriptor allows (`output_dimension` for
  Voyage, `dimensions` for OpenAI), so the caller never picks a vendor field name.
- `RerankRequest{Model, Query, Documents, TopK}` → `RerankResponse{Results[]{Index, Score}}`.

## Declaring the dependency

Declare the provider a service consumes in `service.codefly.yaml`, next to
`service-dependencies:`:

```yaml
provider-dependencies:
  - provider: llm
    vendor: voyage
    secret: voyage/api-key
```

At runtime the host resolves the declaration into an admitted origin and a
credential handle for purpose `runtime`, and hands the service an executor:

```go
exec := codefly.For(ctx).Provider("llm") // implements llm.Executor
client := llm.NewClient(exec)
resp, err := client.Embed(ctx, req)
```

> The SDK seam (`codefly.For(ctx).Provider("llm")`) lands in sdk-go, which reads
> the declaration above and builds the bound session. `provider/llm` — the
> manifests, typed requests, and decoders this page describes — is the piece that
> lives in codefly/core.

## Naming the secret

The `runtime` credential is a workspace secret named `<vendor>/api-key`, e.g.
`anthropic/api-key`, `voyage/api-key`, `openai/api-key`. The host injects it into
the outbound request from the credential vault; it is never carried in a request
descriptor or logged. Prompt and input content is screened up front:
`ScreenContent` (and `PlannedChat` / `PlannedEmbed` / `PlannedRerank`) reject
secret-shaped text with `ErrSecretShapedContent` rather than forwarding it to the
broker.

## Testing with the recorder

Model calls record and replay through `provider/cassette`, so a contract suite
runs offline with no network and no key. Point the origin at a loopback server,
record once against it, then replay the marshaled cassette:

```go
m, _ := llm.Manifest(loopbackOrigin, llm.VendorVoyage)
planned, _ := llm.PlannedEmbed(m, admittedOrigin, llm.EmbedRequest{
    Model: "voyage-3", Input: []string{"hello"}, InputType: "document",
}, "idem-embed", policyDigest)

// Record against the loopback server.
rec := cassette.New(cassette.ModeRecord, providerVersion)
recorded, _ := llm.NewClient(session(recordAddr, rec)).Embed(ctx, request)

// Replay the marshaled cassette with no network.
data, _ := rec.Marshal()
replay, _ := cassette.Load(data, providerVersion)
replayed, _ := llm.NewClient(session(closedAddr, replay)).Embed(ctx, request)
```

Replay never falls back to live: an unknown key hard-errors instead of reaching
the network. When a real key is configured, the same test runs in record mode
against the production origin. See `provider/llm/client_test.go` and
`provider/llm/harness_test.go` for the full wiring.
