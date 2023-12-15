package communicate

import (
	"context"

	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/shared"
)

type Communicate interface {
	Communicate(ctx context.Context, req *agentv1.Engage) (*agentv1.InformationRequest, error)
}

func Channel[T any]() *agentv1.Channel {
	return &agentv1.Channel{
		Kind: shared.TypeOf[T](),
	}
}
