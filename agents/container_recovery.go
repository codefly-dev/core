package agents

import (
	"context"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/runners/dockerrun"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func containerRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == agentv0.Agent_GetAgentInformation_FullMethodName {
			if scope := dockerrun.InheritedContainerRecoveryScope(); scope != "" {
				if err := grpc.SetHeader(ctx, metadata.Pairs(dockerrun.ContainerRecoveryScopeHeader, scope)); err != nil {
					return nil, err
				}
			}
		}
		return handler(ctx, req)
	}
}
