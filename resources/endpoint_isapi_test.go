package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

// Endpoint.Proto() sets Api but leaves ApiDetails nil, so the Is* helpers must
// tolerate a nil ApiDetails on an endpoint whose Api matches, rather than panic.
func TestIsAPIHelpersTolerateNilDetails(t *testing.T) {
	ctx := context.Background()

	require.Nil(t, resources.IsGRPC(ctx, &basev0.Endpoint{Api: standards.GRPC}))
	require.Nil(t, resources.IsRest(ctx, &basev0.Endpoint{Api: standards.REST}))
	require.Nil(t, resources.IsHTTP(ctx, &basev0.Endpoint{Api: standards.HTTP}))
	require.Nil(t, resources.IsTCP(ctx, &basev0.Endpoint{Api: standards.TCP}))

	grpc := &basev0.GrpcAPI{}
	ep := &basev0.Endpoint{Api: standards.GRPC, ApiDetails: resources.ToGrpcAPI(grpc)}
	require.Same(t, grpc, resources.IsGRPC(ctx, ep))
}
