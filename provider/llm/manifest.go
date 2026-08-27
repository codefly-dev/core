// Package llm is a thin, CGO-free client surface for LLM chat and embedding
// calls that go through the provider broker. It expresses those calls as a
// provider manifest with request descriptors, origin rules, and response
// schemas, so LLM egress inherits the broker's admission, SSRF hardening,
// credential vault, response-policy secret filtering, and secret-safe
// deterministic cassettes — chat streams over Server-Sent Events, and both the
// streaming and non-streaming paths record and replay through provider/cassette.
//
// The package owns request shaping and typed decoding only; the host owns the
// broker session, the admitted origin, credentials, operation identity, and
// budget.
package llm

import (
	"fmt"

	"github.com/codefly-dev/core/provider/manifest"
)

// Request descriptor and credential identifiers packaged by the manifest.
const (
	// ChatDescriptor is the streaming/non-streaming chat request descriptor.
	ChatDescriptor = "chat"
	// EmbedDescriptor is the embedding request descriptor.
	EmbedDescriptor = "embed"
	// CredentialPurpose is the runtime API-key purpose both descriptors use.
	CredentialPurpose = "runtime"
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

// Manifest builds the Anthropic LLM provider manifest bound to origin. It covers
// chat over /v1/messages (streaming and non-streaming) and embeddings over
// /v1/embeddings.
func Manifest(origin Origin) (*manifest.Manifest, error) {
	yaml := fmt.Sprintf(manifestFmt,
		origin.Scheme, origin.Host, origin.Port, // origin_rule defaults
		origin.Scheme, // origin_rule schemes
		origin.Host,   // origin_rule host_patterns
		origin.Port,   // origin_rule ports
		origin.Class,  // origin_rule private_network_classes
	)
	return manifest.Load([]byte(yaml))
}

// manifestFmt is the origin-templated Anthropic LLM manifest. The chat response
// schema forwards both the non-streaming content text and the streamed text
// deltas, so a single descriptor serves both paths: the broker detects a
// text/event-stream response and streams it, and a JSON response is filtered as
// one body. No field is a capture, so nothing here is secret-bearing — the
// API key is injected by the credential vault, never carried in a descriptor.
const manifestFmt = `
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
    - id: model-embed
      action: model.embed
      resource: "provider:anthropic/${workspace}/${environment}/${binding}/model"
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
    path_template: /v1/messages
    allowed_body_fields: [model, messages, max_tokens, stream, system, temperature]
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
    allowed_body_fields: [model, input]
    request_byte_budget: 262144
    response_byte_budget: 1048576
    read_only: false
    response_schema: embedding
    credential_purposes: [runtime]
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
    minimum_scope: Call the language model on behalf of the binding.
    permitted_consumer: runtime
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
  - id: embedding
    fields:
      - selector: {version: v1, path: "$.data[*].embedding"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.usage.input_tokens"}
        disposition: FORWARD_SAFE
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: false
  replace: false
  delete: false
  stepwise_upgrade: false
diagnostic_namespace: provider.anthropic.
`
