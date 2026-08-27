package llm

import (
	"context"
	"fmt"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/manifest"
)

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

// PlannedChat binds a chat request to a digest-bound PlannedRequest for the
// chat descriptor. The host wraps it in an ExecuteRequestRequest with the
// operation, budget, and credential handles before executing.
func PlannedChat(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, request ChatRequest, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	return plannedPost(m, origin, ChatDescriptor, request.Body(), idempotencyKey, responsePolicyDigest)
}

// PlannedEmbed binds an embedding request to a digest-bound PlannedRequest for
// the embed descriptor.
func PlannedEmbed(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, request EmbedRequest, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	return plannedPost(m, origin, EmbedDescriptor, request.Body(), idempotencyKey, responsePolicyDigest)
}

func plannedPost(m *manifest.Manifest, origin *providerv0.AdmittedOrigin, descriptorID string, body map[string]*providerv0.PublicValue, idempotencyKey, responsePolicyDigest string) (*providerv0.PlannedRequest, error) {
	descriptor, ok := descriptorByID(m, descriptorID)
	if !ok {
		return nil, fmt.Errorf("descriptor %q is not packaged", descriptorID)
	}
	digest, err := manifest.RequestDescriptorDigest(descriptor)
	if err != nil {
		return nil, err
	}
	planned := &providerv0.PlannedRequest{
		RequestDescriptorId:     descriptorID,
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
