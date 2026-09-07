package llm

import (
	"context"
	"errors"
	"fmt"
	"slices"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
)

// ErrSecretShapedContent reports prompt or embedding-input content the provider
// broker refuses to send. LLM egress goes through the secret-safe provider
// broker, whose protocol forbids secret-shaped values in a request by design, so
// content that trips the secret heuristic (a literal API key, a "password=…"
// fragment, a PEM private-key header) cannot be sent through this path. This is
// an inherent property of routing model calls over the broker, surfaced here as
// a clear, screenable error rather than a confusing failure deep in digest
// binding. Callers that must send such content need a channel other than the
// provider broker.
var ErrSecretShapedContent = errors.New("content is secret-shaped and cannot be sent through the provider broker")

// ScreenContent rejects any free-form text the provider secret heuristic would
// flag, using the exact heuristic the broker enforces, so a caller fails fast
// and legibly before a request is built.
func ScreenContent(texts []string) error {
	for _, text := range texts {
		if configuration.LooksSecret(text) {
			return fmt.Errorf("%w", ErrSecretShapedContent)
		}
	}
	return nil
}

// Executor runs one admitted broker request. *broker.Session satisfies it.
type Executor interface {
	Execute(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*providerv0.ExecuteRequestResponse, error)
}

// Client issues typed LLM and embedding calls through an admitted broker
// executor and decodes the filtered result. It owns no admission, credentials,
// or network — the host builds the request envelope and supplies the executor.
type Client struct {
	exec Executor
}

// NewClient wraps a broker executor.
func NewClient(exec Executor) *Client {
	return &Client{exec: exec}
}

// Chat runs a non-streaming chat request and decodes the whole response.
func (c *Client) Chat(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*ChatResponse, error) {
	response, err := c.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	return DecodeChat(response)
}

// ChatStream runs a streaming chat request and decodes the ordered deltas.
func (c *Client) ChatStream(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*ChatStream, error) {
	response, err := c.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	return DecodeChatStream(response)
}

// Embed runs an embedding request and decodes the ordered vectors.
func (c *Client) Embed(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*EmbeddingResponse, error) {
	response, err := c.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	return DecodeEmbedding(response)
}

// Rerank runs a rerank request and decodes the scored results.
func (c *Client) Rerank(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*RerankResponse, error) {
	response, err := c.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	return DecodeRerank(response)
}

// PlannedChat binds a chat request to a digest-bound PlannedRequest for the
// chat descriptor. The host wraps it in an ExecuteRequestRequest with the
// operation, budget, and credential handles before executing.
func PlannedChat(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, request ChatRequest, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	if err := ScreenContent(request.content()); err != nil {
		return nil, err
	}
	descriptor, ok := descriptorByID(m, ChatDescriptor)
	if !ok {
		return nil, fmt.Errorf("descriptor %q is not packaged", ChatDescriptor)
	}
	return plannedPost(origin, descriptor, request.body(), idempotencyKey, responsePolicyDigest)
}

// PlannedEmbed binds an embedding request to a digest-bound PlannedRequest for
// the embed descriptor. Dimensions, when set, is rendered under whichever field
// name the target descriptor allows (output_dimension for Voyage, dimensions for
// OpenAI).
func PlannedEmbed(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, request EmbedRequest, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	if err := ScreenContent(request.content()); err != nil {
		return nil, err
	}
	descriptor, ok := descriptorByID(m, EmbedDescriptor)
	if !ok {
		return nil, fmt.Errorf("descriptor %q is not packaged", EmbedDescriptor)
	}
	body := request.body()
	if request.Dimensions != 0 {
		field, ok := dimensionField(descriptor)
		if !ok {
			return nil, fmt.Errorf("descriptor %q does not accept a dimension field", EmbedDescriptor)
		}
		body[field] = integerValue(request.Dimensions)
	}
	return plannedPost(origin, descriptor, body, idempotencyKey, responsePolicyDigest)
}

// PlannedRerank binds a rerank request to a digest-bound PlannedRequest for the
// rerank descriptor.
func PlannedRerank(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, request RerankRequest, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	if err := ScreenContent(request.content()); err != nil {
		return nil, err
	}
	descriptor, ok := descriptorByID(m, RerankDescriptor)
	if !ok {
		return nil, fmt.Errorf("descriptor %q is not packaged", RerankDescriptor)
	}
	return plannedPost(origin, descriptor, request.body(), idempotencyKey, responsePolicyDigest)
}

// dimensionField reports which vendor-specific dimension body field a descriptor
// allows.
func dimensionField(descriptor manifest.RequestDescriptor) (string, bool) {
	for _, field := range descriptor.AllowedBodyFields {
		if field == "output_dimension" || field == "dimensions" {
			return field, true
		}
	}
	return "", false
}

func plannedPost(origin *providerv0.AdmittedOrigin, descriptor manifest.RequestDescriptor, body map[string]*providerv0.PublicValue, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	for key := range body {
		if !slices.Contains(descriptor.AllowedBodyFields, key) {
			return nil, fmt.Errorf("body field %q is not allowed by the %q descriptor", key, descriptor.ID)
		}
	}
	digest, err := manifest.RequestDescriptorDigest(descriptor)
	if err != nil {
		return nil, err
	}
	planned := &providerv0.PlannedRequest{
		RequestDescriptorId:     descriptor.ID,
		RequestDescriptorDigest: digest,
		Method:                  providerv0.HTTPMethod_HTTP_METHOD_POST,
		AdmittedOriginDigest:    origin.GetAdmissionDigest(),
		Body:                    body,
		CredentialPurposes:      []providerv0.CredentialPurpose{providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME},
		ResponsePolicyDigest:    responsePolicyDigest,
		IdempotencyKey:          idempotencyKey,
	}
	return canonical.BindPlannedRequestDigest(planned)
}

func descriptorByID(m *manifest.Manifest, id string) (manifest.RequestDescriptor, bool) {
	for _, descriptor := range m.Requests {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return manifest.RequestDescriptor{}, false
}
