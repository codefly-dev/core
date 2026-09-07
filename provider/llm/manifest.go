// Package llm is a thin, CGO-free client surface for LLM chat, embedding, and
// rerank calls that go through the provider broker. It expresses those calls as
// per-vendor provider manifests with request descriptors, origin rules, and
// response schemas, so LLM egress inherits the broker's admission, SSRF
// hardening, credential vault, response-policy secret filtering, and secret-safe
// deterministic cassettes — chat streams over Server-Sent Events, and both the
// streaming and non-streaming paths record and replay through provider/cassette.
//
// The package owns request shaping and typed decoding only; the host owns the
// broker session, the admitted origin, credentials, operation identity, and
// budget.
//
// Vendors differ in which capabilities they serve: Anthropic serves chat but has
// no embeddings endpoint, its embeddings partner Voyage serves embed and rerank,
// and OpenAI serves both chat and embed. Manifest is parameterized by vendor so a
// service fronts exactly the descriptors its vendor actually serves.
//
// The provider protocol forbids secret-shaped values in a request by design, so
// prompt or embedding-input content that trips the secret heuristic cannot be
// sent through the broker. PlannedChat, PlannedEmbed, and PlannedRerank screen
// content up front and return ErrSecretShapedContent, making that inherent
// constraint a clear, screenable error rather than a confusing failure deep in
// digest binding.
package llm

import (
	"fmt"

	"github.com/codefly-dev/core/provider/manifest"
)

// Request descriptor and credential identifiers packaged by the manifests.
const (
	// ChatDescriptor is the streaming/non-streaming chat request descriptor.
	ChatDescriptor = "chat"
	// EmbedDescriptor is the embedding request descriptor.
	EmbedDescriptor = "embed"
	// RerankDescriptor is the rerank request descriptor.
	RerankDescriptor = "rerank"
	// CredentialPurpose is the runtime API-key purpose every descriptor uses.
	CredentialPurpose = "runtime"
)

// Vendor selects which model vendor a manifest fronts. Each vendor advertises
// only the descriptors it actually serves.
type Vendor string

const (
	// VendorAnthropic serves chat over /v1/messages; it has no embeddings endpoint.
	VendorAnthropic Vendor = "anthropic"
	// VendorVoyage serves embeddings over /v1/embeddings and rerank over /v1/rerank.
	VendorVoyage Vendor = "voyage"
	// VendorOpenAI serves chat over /v1/chat/completions and embeddings over /v1/embeddings.
	VendorOpenAI Vendor = "openai"
)

// Origin is the network origin the manifest targets. It is configurable so the
// same manifest can front the real vendor host, a local gateway or proxy, or —
// for offline record/replay tests — a loopback server, without a network call.
type Origin struct {
	Scheme string
	Host   string
	Port   uint32
	// Class is the manifest private-network class token: loopback, link-local,
	// private, or public.
	Class string
}

// AnthropicOrigin is the production Anthropic API origin.
func AnthropicOrigin() Origin {
	return Origin{Scheme: "https", Host: "api.anthropic.com", Port: 443, Class: "public"}
}

// VoyageOrigin is the production Voyage AI API origin.
func VoyageOrigin() Origin {
	return Origin{Scheme: "https", Host: "api.voyageai.com", Port: 443, Class: "public"}
}

// OpenAIOrigin is the production OpenAI API origin.
func OpenAIOrigin() Origin {
	return Origin{Scheme: "https", Host: "api.openai.com", Port: 443, Class: "public"}
}

// Manifest builds the LLM provider manifest for vendor, bound to origin. The
// descriptor set is vendor-specific: Anthropic packages chat only, Voyage
// packages embed and rerank, and OpenAI packages chat and embed.
func Manifest(origin Origin, vendor Vendor) (*manifest.Manifest, error) {
	head, ok := vendorHeads[vendor]
	if !ok {
		return nil, fmt.Errorf("unknown vendor %q", vendor)
	}
	return manifest.Load([]byte(head + originTrailer(origin)))
}

// originTrailer renders the shared origin rule, credential purpose, sandbox, and
// state blocks with origin templated in. No field is a capture, so nothing here
// is secret-bearing — the API key is injected by the credential vault, never
// carried in a descriptor.
func originTrailer(origin Origin) string {
	return fmt.Sprintf(originTrailerFmt,
		origin.Scheme, origin.Host, origin.Port, // origin_rule defaults
		origin.Scheme, // origin_rule schemes
		origin.Host,   // origin_rule host_patterns
		origin.Port,   // origin_rule ports
		origin.Class,  // origin_rule private_network_classes
	)
}

var vendorHeads = map[Vendor]string{
	VendorAnthropic: anthropicHead,
	VendorVoyage:    voyageHead,
	VendorOpenAI:    openaiHead,
}

const originTrailerFmt = `
origin_rules:
  - id: api
    defaults: [%s://%s:%d]
    schemes: [%s]
    host_patterns: [%s]
    ports: [%d]
    binding_override: within-rule
    private_network_classes: [%s]
credential_purposes:
  - id: runtime
    minimum_scope: Call the model on behalf of the binding.
    permitted_consumer: runtime
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: false
  replace: false
  delete: false
  stepwise_upgrade: false
`

// anthropicHead packages chat over /v1/messages. Anthropic serves no embeddings
// endpoint, so no embed descriptor is advertised. The chat response schema
// forwards both the non-streaming content text and the streamed text deltas, so a
// single descriptor serves both paths.
const anthropicHead = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: anthropic
  version: 0.1.0
default_deletion_policy: retain
permissions:
  required:
    - id: model-invoke
      action: model.invoke
      resource: "provider:anthropic/${workspace}/${environment}/${binding}/model"
      resource_type: model
      reason: Invoke the language model for a chat completion.
      risk: medium
      credential_purpose: runtime
resource_types:
  - id: model
    actions: [create]
requests:
  - id: chat
    permissions: [model-invoke]
    resource_type: model
    action: create
    origin_rule: api
    operation: invoke
    method: POST
    path_template: /v1/messages
    allowed_body_fields: [model, messages, max_tokens, stream, system, temperature]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: chat
    credential_purposes: [runtime]
response_schemas:
  - id: chat
    fields:
      - selector: {version: v1, path: "$.content[*].text"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.stop_reason"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.input_tokens"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.output_tokens"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.delta.text"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.delta.stop_reason"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.message.usage.input_tokens"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.message.usage.output_tokens"}
        disposition: FORWARD_SAFE
diagnostic_namespace: provider.anthropic.
`

// voyageHead packages embeddings over /v1/embeddings (with input_type and
// output_dimension) and rerank over /v1/rerank. Both response schemas forward
// $.data[*] element fields and the total-token usage Voyage reports.
const voyageHead = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: voyage
  version: 0.1.0
default_deletion_policy: retain
permissions:
  required:
    - id: model-embed
      action: model.embed
      resource: "provider:voyage/${workspace}/${environment}/${binding}/model"
      resource_type: model
      reason: Compute embeddings for the given input.
      risk: medium
      credential_purpose: runtime
    - id: model-rerank
      action: model.rerank
      resource: "provider:voyage/${workspace}/${environment}/${binding}/model"
      resource_type: model
      reason: Rerank documents against the given query.
      risk: medium
      credential_purpose: runtime
resource_types:
  - id: model
    actions: [update]
requests:
  - id: embed
    permissions: [model-embed]
    resource_type: model
    action: update
    origin_rule: api
    operation: embed
    method: POST
    path_template: /v1/embeddings
    allowed_body_fields: [model, input, input_type, output_dimension]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: embedding
    credential_purposes: [runtime]
  - id: rerank
    permissions: [model-rerank]
    resource_type: model
    action: update
    origin_rule: api
    operation: rerank
    method: POST
    path_template: /v1/rerank
    allowed_body_fields: [model, query, documents, top_k]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: rerank
    credential_purposes: [runtime]
response_schemas:
  - id: embedding
    fields:
      - selector: {version: v1, path: "$.data[*].embedding"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.total_tokens"}
        disposition: FORWARD_SAFE
  - id: rerank
    fields:
      - selector: {version: v1, path: "$.data[*].index"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.data[*].relevance_score"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.total_tokens"}
        disposition: FORWARD_SAFE
diagnostic_namespace: provider.voyage.
`

// openaiHead packages chat over /v1/chat/completions and embeddings over
// /v1/embeddings (with dimensions). The chat schema forwards both the
// non-streaming message content and the streamed content deltas; the embedding
// schema forwards the prompt- and total-token usage OpenAI reports.
const openaiHead = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: openai
  version: 0.1.0
default_deletion_policy: retain
permissions:
  required:
    - id: model-invoke
      action: model.invoke
      resource: "provider:openai/${workspace}/${environment}/${binding}/model"
      resource_type: model
      reason: Invoke the language model for a chat completion.
      risk: medium
      credential_purpose: runtime
    - id: model-embed
      action: model.embed
      resource: "provider:openai/${workspace}/${environment}/${binding}/model"
      resource_type: model
      reason: Compute embeddings for the given input.
      risk: medium
      credential_purpose: runtime
resource_types:
  - id: model
    actions: [create, update]
requests:
  - id: chat
    permissions: [model-invoke]
    resource_type: model
    action: create
    origin_rule: api
    operation: invoke
    method: POST
    path_template: /v1/chat/completions
    allowed_body_fields: [model, messages, max_tokens, stream, temperature]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: chat
    credential_purposes: [runtime]
  - id: embed
    permissions: [model-embed]
    resource_type: model
    action: update
    origin_rule: api
    operation: embed
    method: POST
    path_template: /v1/embeddings
    allowed_body_fields: [model, input, dimensions]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: embedding
    credential_purposes: [runtime]
response_schemas:
  - id: chat
    fields:
      - selector: {version: v1, path: "$.choices[*].message.content"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.choices[*].delta.content"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.choices[*].finish_reason"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.prompt_tokens"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.completion_tokens"}
        disposition: FORWARD_SAFE
  - id: embedding
    fields:
      - selector: {version: v1, path: "$.data[*].embedding"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.prompt_tokens"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.total_tokens"}
        disposition: FORWARD_SAFE
diagnostic_namespace: provider.openai.
`
