package wool_test

import (
	"context"
	"testing"

	metadata "google.golang.org/grpc/metadata"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/wool"
)

func TestUser(t *testing.T) {
	ctx := context.Background()
	w := wool.Get(ctx).In("UpdateWorkspace")

	authID := "test-auth-id"
	w.WithUserAuthID(authID)

	id, ok := w.UserAuthID()
	require.True(t, ok)
	require.Equal(t, authID, id)

	// HTTP
	hs := w.HTTP().Headers()
	require.Equal(t, []string{authID}, hs[wool.Header(wool.UserAuthIDKey)])

	// GRPC
	md := metadata.New(map[string]string{})
	md.Set(string(wool.UserAuthIDKey), authID)
	grpCtxIn := metadata.NewIncomingContext(ctx, md)

	w = wool.Get(grpCtxIn).In("UpdateWorkspace").GRPC().Inject()
	id, ok = w.UserAuthID()
	require.True(t, ok)
	require.Equal(t, authID, id)
}
