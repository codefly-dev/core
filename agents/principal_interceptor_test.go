package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/policy"
)

func encodedPrincipal(t *testing.T, id, org string) string {
	t.Helper()
	token, err := policy.EncodePrincipalToken(&policy.Principal{
		ID: id, Kind: policy.KindHuman, OrgID: org,
	})
	require.NoError(t, err)
	return token
}

func TestPrincipalInterceptorRejectsPerCallAuthorityReplacement(t *testing.T) {
	t.Setenv("CODEFLY_PRINCIPAL_TOKEN", encodedPrincipal(t, "spawn-principal", "org-1"))
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		PrincipalMetadataKey, encodedPrincipal(t, "attacker-principal", "org-2"),
	))
	called := false
	_, err := principalUnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/fixture/Call"},
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestPrincipalInterceptorRejectsMalformedSpawnBinding(t *testing.T) {
	t.Setenv("CODEFLY_PRINCIPAL_TOKEN", "not-a-principal-token")
	called := false
	_, err := principalUnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/fixture/Call"},
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, called)
}

func TestPrincipalInterceptorStampsOnlySpawnBoundPrincipal(t *testing.T) {
	t.Setenv("CODEFLY_PRINCIPAL_TOKEN", encodedPrincipal(t, "spawn-principal", "org-1"))
	_, err := principalUnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/fixture/Call"},
		func(ctx context.Context, _ any) (any, error) {
			principal := policy.PrincipalFrom(ctx)
			require.NotNil(t, principal)
			require.Equal(t, "spawn-principal", principal.ID)
			require.Equal(t, "org-1", principal.OrgID)
			return nil, nil
		})
	require.NoError(t, err)
}
