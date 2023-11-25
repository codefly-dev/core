package communicate

import (
	pluginsv1 "github.com/codefly-dev/core/proto/v1/go/plugins"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	"github.com/codefly-dev/core/shared"
)

type QuestionHandler interface {
	Process(req *pluginsv1.InformationRequest) (*pluginsv1.Answer, error)
}

type ServerContext struct {
	Handler QuestionHandler
	Method  pluginsv1.Method
	logger  shared.BaseLogger
	done    bool
}

func (c *ServerContext) Done() bool {
	return c.done
}

func (c *ServerContext) Communicate(answer *pluginsv1.Answer) (*pluginsv1.Engage, error) {
	return &pluginsv1.Engage{Method: c.Method, Answer: answer}, nil
}

func (c *ServerContext) Process(request *pluginsv1.InformationRequest) (*pluginsv1.Answer, error) {
	return c.Handler.Process(request)
}

func NewServerContext(method pluginsv1.Method, logger shared.BaseLogger) *ServerContext {
	return &ServerContext{
		Method: method,
		logger: logger,
	}
}

type ServerManager struct {
	channels map[pluginsv1.Method]*ServerContext
	logger   shared.BaseLogger
}

func (m *ServerManager) Register(channels ...*pluginsv1.Channel) error {
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

func ToMethod(req any) pluginsv1.Method {
	switch req.(type) {
	case factoryv1.CreateRequest:
		return Create
	case factoryv1.SyncRequest:
		return Sync
	default:
		return pluginsv1.Method_UNKNOWN
	}
}

func NewServerManager(logger shared.BaseLogger) *ServerManager {
	return &ServerManager{
		logger:   logger,
		channels: make(map[pluginsv1.Method]*ServerContext),
	}
}
