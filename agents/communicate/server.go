package communicate

import (
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	"github.com/codefly-dev/core/shared"
)

type QuestionHandler interface {
	Process(req *agentsv1.InformationRequest) (*agentsv1.Answer, error)
}

type ServerContext struct {
	Handler QuestionHandler
	Method  agentsv1.Method
	logger  shared.BaseLogger
	done    bool
}

func (c *ServerContext) Done() bool {
	return c.done
}

func (c *ServerContext) Communicate(answer *agentsv1.Answer) (*agentsv1.Engage, error) {
	return &agentsv1.Engage{Method: c.Method, Answer: answer}, nil
}

func (c *ServerContext) Process(request *agentsv1.InformationRequest) (*agentsv1.Answer, error) {
	return c.Handler.Process(request)
}

func NewServerContext(method agentsv1.Method, logger shared.BaseLogger) *ServerContext {
	return &ServerContext{
		Method: method,
		logger: logger,
	}
}

type ServerManager struct {
	channels map[agentsv1.Method]*ServerContext
	logger   shared.BaseLogger
}

func (m *ServerManager) Register(channels ...*agentsv1.Channel) error {
	for _, c := range channels {
		m.channels[c.Method] = NewServerContext(c.Method, m.logger)
	}
	return nil
}

func (m *ServerManager) RequiresCommunication(req any) (*ServerContext, bool) {
	method := ToMethod(req)
	if s, ok := m.channels[method]; ok {
		return s, true
	}
	return nil, false
}

func ToMethod(req any) agentsv1.Method {
	switch req.(type) {
	case factoryv1.CreateRequest:
		return Create
	case factoryv1.SyncRequest:
		return Sync
	default:
		return agentsv1.Method_UNKNOWN
	}
}

func NewServerManager(logger shared.BaseLogger) *ServerManager {
	return &ServerManager{
		logger:   logger,
		channels: make(map[agentsv1.Method]*ServerContext),
	}
}
