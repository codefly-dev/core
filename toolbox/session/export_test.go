package session

import (
	"context"
	"time"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"google.golang.org/grpc"
)

// CallWithClock lets the real-process tests advance past the caller's deadline
// while its timer notification remains pending. It is absent from library builds.
func (s *ToolboxSession) CallWithClock(ctx context.Context, input CallRequest, now func() time.Time) (*CallResult, error) {
	return s.call(ctx, input, now)
}

// AfterDispatch preserves the real RPC and runs a gate before session accepts
// its response. No transport status or fixture result is manufactured.
func (s *ToolboxSession) AfterDispatch(gate func()) {
	s.plugin.Client = &dispatchGate{ToolboxClient: s.plugin.Client, gate: gate}
}

type dispatchGate struct {
	toolboxv0.ToolboxClient
	gate func()
}

func (d *dispatchGate) CallTool(ctx context.Context, request *toolboxv0.CallToolRequest, opts ...grpc.CallOption) (*toolboxv0.CallToolResponse, error) {
	response, err := d.ToolboxClient.CallTool(ctx, request, opts...)
	d.gate()
	return response, err
}
