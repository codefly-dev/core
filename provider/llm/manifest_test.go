package llm_test

import (
	"testing"

	"github.com/codefly-dev/core/provider/llm"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/stretchr/testify/require"
)

// descriptorIDs returns the set of request descriptor ids the manifest packages,
// proving each digest binds along the way.
func descriptorIDs(t *testing.T, m *manifest.Manifest) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, descriptor := range m.Requests {
		ids[descriptor.ID] = true
		_, err := manifest.RequestDescriptorDigest(descriptor)
		require.NoError(t, err)
	}
	return ids
}

// TestManifest_Anthropic proves the Anthropic manifest validates against the real
// origin and packages chat only — it no longer advertises an embed descriptor it
// cannot serve.
func TestManifest_Anthropic(t *testing.T) {
	m, err := llm.Manifest(llm.AnthropicOrigin(), llm.VendorAnthropic)
	require.NoError(t, err)

	ids := descriptorIDs(t, m)
	require.True(t, ids[llm.ChatDescriptor])
	require.False(t, ids[llm.EmbedDescriptor])
	require.False(t, ids[llm.RerankDescriptor])
}

// TestManifest_Voyage proves the Voyage manifest validates and packages embed and
// rerank, with the vendor-specific body fields Voyage serves.
func TestManifest_Voyage(t *testing.T) {
	m, err := llm.Manifest(llm.VoyageOrigin(), llm.VendorVoyage)
	require.NoError(t, err)

	ids := descriptorIDs(t, m)
	require.True(t, ids[llm.EmbedDescriptor])
	require.True(t, ids[llm.RerankDescriptor])
	require.False(t, ids[llm.ChatDescriptor])

	require.Contains(t, allowedBodyFields(t, m, llm.EmbedDescriptor), "input_type")
	require.Contains(t, allowedBodyFields(t, m, llm.EmbedDescriptor), "output_dimension")
	rerank := allowedBodyFields(t, m, llm.RerankDescriptor)
	require.Contains(t, rerank, "query")
	require.Contains(t, rerank, "documents")
	require.Contains(t, rerank, "top_k")
}

// TestManifest_OpenAI proves the OpenAI manifest validates and packages chat and
// embed, with the dimensions body field OpenAI serves.
func TestManifest_OpenAI(t *testing.T) {
	m, err := llm.Manifest(llm.OpenAIOrigin(), llm.VendorOpenAI)
	require.NoError(t, err)

	ids := descriptorIDs(t, m)
	require.True(t, ids[llm.ChatDescriptor])
	require.True(t, ids[llm.EmbedDescriptor])
	require.False(t, ids[llm.RerankDescriptor])

	require.Contains(t, allowedBodyFields(t, m, llm.EmbedDescriptor), "dimensions")
}

// TestManifest_UnknownVendor rejects a vendor with no packaged manifest.
func TestManifest_UnknownVendor(t *testing.T) {
	_, err := llm.Manifest(llm.AnthropicOrigin(), llm.Vendor("gemini"))
	require.Error(t, err)
}

func allowedBodyFields(t *testing.T, m *manifest.Manifest, id string) []string {
	t.Helper()
	for _, descriptor := range m.Requests {
		if descriptor.ID == id {
			return descriptor.AllowedBodyFields
		}
	}
	t.Fatalf("descriptor %q not packaged", id)
	return nil
}
