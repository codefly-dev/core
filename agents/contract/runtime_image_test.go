package contract

import (
	"strings"
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestRuntimeInitImageWire(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image string
	}{
		{name: "absent on deployed paths"},
		{name: "explicit tag", image: "registry.example.test:5000/team/service:dev-123"},
		{name: "sha256 digest", image: "registry.example.test/team/service@sha256:" + strings.Repeat("a", 64)},
		// Core carries input unchanged; the consuming agent must refuse this.
		{name: "policy belongs to the agent", image: "service:latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &runtimev0.InitRequest{RuntimeImage: tc.image}
			wire, err := proto.Marshal(request)
			require.NoError(t, err)
			// Field 10 is length-delimited (0x52). These fixtures fit a one-byte
			// length. Pin the wire slot independently of the generated descriptor.
			expected := []byte{}
			if tc.image != "" {
				expected = append([]byte{0x52, byte(len(tc.image))}, []byte(tc.image)...)
			}
			require.Equal(t, expected, wire)
			var restored runtimev0.InitRequest
			require.NoError(t, proto.Unmarshal(wire, &restored))
			require.Equal(t, tc.image, restored.GetRuntimeImage())
			require.True(t, proto.Equal(request, &restored))
		})
	}
	// Baseline servers must not imply adoption just by linking this schema.
	require.ErrorIs(t, Check(Current(), RuntimeInitImage), ErrIncompatible)
	adopted := Current()
	adopted.Capabilities = append(adopted.Capabilities, RuntimeInitImage)
	require.NoError(t, Check(adopted, RuntimeInitImage))
}
