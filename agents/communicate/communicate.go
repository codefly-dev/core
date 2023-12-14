package communicate

import (
	"context"
	agentsv1 "github.com/codefly-dev/core/generated/v1/go/proto/agents"
	"github.com/codefly-dev/core/shared"
)

type Communicate interface {
	Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error)
}

func Channel[T any]() *agentsv1.Channel {
	return &agentsv1.Channel{
		Kind: shared.TypeOf[T](),
	}
}
