package communicate

import (
	"context"
	"fmt"

	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// Server is the Agent
type Server struct {
	channels map[string]*ServerContext
}

func (server *Server) Log(ctx context.Context) {
	w := wool.Get(ctx).In("communicate.Server.State")
	for k, v := range server.channels {
		w.Focus("channel", wool.NameField(k), wool.Field("context", v))
	}
}

type ServerContext struct {
	done    bool
	gen     QuestionGenerator
	session *ServerSession
}

func (c *ServerContext) Done() bool {
	return c.done
}

func (c *ServerContext) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	w := wool.Get(ctx).In("communicate.Server.Communicate")
	if req.Mode == agentv0.Engage_START {
		w.Focus("FUCK")
		c.session = NewServerSession(c.gen)
	}
	return c.session.Process(ctx, req)
}

func NewServerContext(_ context.Context, gen QuestionGenerator) *ServerContext {
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

func (server *Server) Register(ctx context.Context, generator *Generator) error {
	server.channels[generator.Kind] = NewServerContext(ctx, generator.QuestionGenerator)
	return nil
}

func (server *Server) RequiresCommunication(channel *agentv0.Channel) (*ServerContext, bool) {
	if s, ok := server.channels[channel.Kind]; ok {
		return s, true
	}
	return nil, false

}

func (server *Server) Ready(s string) bool {
	if c, ok := server.channels[s]; ok {
		return c.Done()
	}
	return true
}

// Communicate from the generator and sends back information request required
func (server *Server) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	w := wool.Get(ctx).In("communicate.Server.Communicate")
	w.Trace("channels available", wool.Field("keys", server.Channels()))
	if c, ok := server.channels[req.Channel.Kind]; ok {
		w.Trace("communicating in channel", wool.NameField(req.Channel.Kind))
		return c.Communicate(ctx, req)
	}
	return &agentv0.InformationRequest{Done: true}, nil
}

func (server *Server) Done(ctx context.Context, channel *agentv0.Channel) (*ServerSession, error) {
	w := wool.Get(ctx).In("communicate.Server.Done")
	if c, ok := server.channels[channel.Kind]; ok {
		if c.session == nil {
			return nil, w.NewError("cannot find session for channel %s", channel.Kind)
		}
		return c.session, nil
	}
	return nil, nil
}

func (server *Server) Channels() []string {
	var channels []string
	for c := range server.channels {
		channels = append(channels, c)
	}
	return channels
}

func NewServer(_ context.Context) *Server {
	return &Server{
		channels: make(map[string]*ServerContext),
	}
}

type QuestionGenerator interface {
	Ready() bool
	Process(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error)
}

type ServerSession struct {
	generator QuestionGenerator
	states    map[string]*agentv0.Answer
}

func (session *ServerSession) GetState() map[string]*agentv0.Answer {
	return session.states
}

func NewServerSession(generator QuestionGenerator) *ServerSession {
	return &ServerSession{
		generator: generator,
		states:    make(map[string]*agentv0.Answer),
	}
}

var _ QuestionGenerator = &ServerSession{}

func (session *ServerSession) Ready() bool {
	return false
}

func (session *ServerSession) Process(ctx context.Context, eng *agentv0.Engage) (*agentv0.InformationRequest, error) {
	if eng.Answer != nil {
		if _, ok := session.states[eng.Stage]; ok {
			return nil, fmt.Errorf("cannot process stage %s twice", eng.Stage)
		}
		session.states[eng.Stage] = eng.Answer
	}
	return session.generator.Process(ctx, eng)
}
