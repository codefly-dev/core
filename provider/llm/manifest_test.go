package llm_test

import (
	"testing"

	"github.com/codefly-dev/core/provider/llm"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/stretchr/testify/require"
)

// TestManifest_ProductionAnthropicIsValid proves the shipped manifest validates
// against the real Anthropic origin and packages both request descriptors.
func TestManifest_ProductionAnthropicIsValid(t *testing.T) {
	m, err := llm.Manifest(llm.AnthropicOrigin())
	require.NoError(t, err)

	ids := map[string]bool{}
	for _, descriptor := range m.Requests {
		ids[descriptor.ID] = true
		_, err := manifest.RequestDescriptorDigest(descriptor)
		require.NoError(t, err)
	}
	require.True(t, ids[llm.ChatDescriptor])
	require.True(t, ids[llm.EmbedDescriptor])
}
