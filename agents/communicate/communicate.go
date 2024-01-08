package communicate

import (
	"context"

	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/codefly-dev/core/shared"
)

type Communicate interface {
	Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error)
}

func Channel[T any]() *agentv0.Channel {
	return &agentv0.Channel{
		Kind: shared.TypeOf[T](),
	}
}
