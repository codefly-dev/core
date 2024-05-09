package wool_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/metadata"

	"github.com/codefly-dev/core/wool"
)

func TestSanitizeForward(t *testing.T) {
	tcs := []struct {
		header string
		wanted string
	}{
		{"User-Agent", "user.agent"},
		{"X-Codefly-Forwarded-For", "codefly.forwarded.for"},
	}
	for _, tc := range tcs {
		t.Run(tc.header, func(t *testing.T) {
			got := wool.HeaderKey(tc.header)
			if got != tc.wanted {
				t.Errorf("got %s, wanted %s", got, tc.wanted)
			}
		})
	}

}

func TestInjectMetaData(t *testing.T) {
	tcs := []struct {
		md     map[string]string
		authID string
		email  string
	}{
		{
			md: map[string]string{
				"codefly.user.auth.id": "123",
				"codefly.user.email":   "test@test.com",
			},
		},
	}
	for _, tc := range tcs {
		t.Run("InjectMetadata", func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.New(tc.md))
			ctx = wool.InjectMetadata(ctx)
			w := wool.Get(ctx)
			authID, err := w.UserAuthID()
			require.NoError(t, err)
			require.Equal(t, "123", authID)
			email, err := w.UserEmail()
			require.NoError(t, err)
			require.Equal(t, "test@test.com", email)
		})
	}
}
