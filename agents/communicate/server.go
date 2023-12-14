package communicate

import (
	"context"
	"fmt"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	"github.com/codefly-dev/core/shared"
)

// Server is the Agent
type Server struct {
	channels map[string]*ServerContext
}

type ServerContext struct {
	done    bool
	gen     QuestionGenerator
	session *ServerSession
}

func (c *ServerContext) Done() bool {
	return c.done
}

func (c *ServerContext) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if req.Mode == agentsv1.Engage_START {
		c.session = NewServerSession(c.gen)
	}
	return c.session.Process(ctx, req)
}

func NewServerContext(ctx context.Context, gen QuestionGenerator) *ServerContext {
	return &ServerContext{gen: gen}
}

type Generator struct {
	Kind              string
	QuestionGenerator QuestionGenerator
}

func New[T any](gen QuestionGenerator) *Generator {
	return &Generator{
		QuestionGenerator: gen,
		Kind:              shared.TypeOf[T](),
	}
}

func (m *Server) Register(ctx context.Context, generator *Generator) error {
	m.channels[generator.Kind] = NewServerContext(ctx, generator.QuestionGenerator)
	return nil
}

func (m *Server) RequiresCommunication(channel *agentsv1.Channel) (*ServerContext, bool) {
	if s, ok := m.channels[channel.Kind]; ok {
		return s, true
	}
	return nil, false

}

func (m *Server) Ready(s string) bool {
	if c, ok := m.channels[s]; ok {
		return c.Done()
	}
	return true
}

// Communicate from the generator and sends back information request required
func (m *Server) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if c, ok := m.channels[req.Channel.Kind]; ok {
		return c.Communicate(ctx, req)
	}
	return nil, fmt.Errorf("cannot find channel %s", req.Channel.Kind)
}

func (m *Server) Done(ctx context.Context, channel *agentsv1.Channel) (*ServerSession, error) {
	logger := shared.GetLogger(ctx).With("communicate.Server.Done")
	if c, ok := m.channels[channel.Kind]; ok {
		if c.session == nil {
			return nil, logger.Errorf("cannot find session for channel %s", channel.Kind)
		}
		return c.session, nil
	}
	return nil, logger.Errorf("cannot find channel %s", channel.Kind)
}

func NewServer(ctx context.Context) *Server {
	return &Server{
		channels: make(map[string]*ServerContext),
	}
}

type QuestionGenerator interface {
	Ready() bool
	Process(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error)
}

type ServerSession struct {
	generator QuestionGenerator
	states    map[string]*agentsv1.Answer
}

func NewServerSession(generator QuestionGenerator) *ServerSession {
	return &ServerSession{
		generator: generator,
		states:    make(map[string]*agentsv1.Answer),
	}
}

var _ QuestionGenerator = &ServerSession{}

func (c *ServerSession) Ready() bool {
	return false
}

func (c *ServerSession) Process(ctx context.Context, eng *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if eng.Answer != nil {
		if _, ok := c.states[eng.Stage]; ok {
			return nil, fmt.Errorf("cannot process stage %s twice", eng.Stage)
		}
		c.states[eng.Stage] = eng.Answer
	}
	return c.generator.Process(ctx, eng)
}
