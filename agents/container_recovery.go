package agents

import (
	"context"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func containerRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == agentv0.Agent_GetAgentInformation_FullMethodName {
			if scope := recoveryscope.Acknowledgement(); scope != "" {
				if err := grpc.SetHeader(ctx, metadata.Pairs(recoveryscope.Header, scope)); err != nil {
					return nil, err
				}
			}
		}
		response, err := handler(ctx, req)
		if err == nil && info.FullMethod == agentv0.Agent_GetAgentInformation_FullMethodName {
			// Agent handlers may return shared metadata across concurrent calls.
			advertisement := proto.Clone(response.(*agentv0.AgentInformation)).(*agentv0.AgentInformation)
			if advertisement.Contract == nil {
				advertisement.Contract = contract.Current()
			}
			return advertisement, nil
		}
		return response, err
	}
}
