package communicate

import (
	"context"
	"fmt"
	"strings"

	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	"github.com/codefly-dev/core/shared"
)

// A Client receives an engagement request and returns an information request
// Often, the request will be a question, and the response will be received as part of the next engagement

type Client interface {
	Ready() bool
	Process(req *agentsv1.Engage) (*agentsv1.InformationRequest, error)
}

type ClientContext struct {
	Method agentsv1.Method
	Client Client
	round  int32
	states []*agentsv1.Answer
	ctx    context.Context
}

var _ Client = &ClientContext{}

func (c *ClientContext) NextRound() int32 {
	c.round++
	return c.round
}

func NewClientContext(ctx context.Context, method agentsv1.Method) (*ClientContext, error) {
	return &ClientContext{
		Method: method,
		ctx:    ctx,
	}, nil
}

// NewSequence creates a SequenceClient
func (c *ClientContext) NewSequence(qs ...*agentsv1.Question) (*Sequence, error) {
	seq := NewSequence(c.Method, qs...)
	c.Client = seq
	return seq, nil
}

func (c *ClientContext) Ready() bool {
	return c.Client.Ready()
}

func (c *ClientContext) Process(eng *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if eng.Answer != nil {
		c.states = append(c.states, eng.Answer)
	}
	return c.Client.Process(eng)
}

func (c *ClientContext) Confirm(s int) *agentsv1.ConfirmAnswer {
	answer := c.states[s]
	if answer == nil {
		return nil
	}
	return answer.GetConfirm()
}

func (c *ClientContext) SafeConfirm(s int) (*agentsv1.ConfirmAnswer, error) {
	if len(c.states) < s {
		return nil, fmt.Errorf("no state for %d", s)
	}
	answer := c.states[s]
	if answer == nil {
		return nil, fmt.Errorf("no state for %d", s)
	}
	back := answer.GetConfirm()
	if back == nil {
		return nil, fmt.Errorf("state is not of the confirm type for %d", s)
	}
	return back, nil
}

func (c *ClientContext) Selection(i int) *agentsv1.SelectionAnswer {
	answer := c.states[i]
	if answer == nil {
		return nil
	}
	return answer.GetSelection()
}

func (c *ClientContext) Input(i int) *agentsv1.InputAnswer {
	answer := c.states[i]
	if answer == nil {
		return nil
	}
	return answer.GetInput()
}

func StateAsString(s *agentsv1.Answer) string {
	switch s.Value.(type) {
	case *agentsv1.Answer_Confirm:
		return s.GetConfirm().String()
	case *agentsv1.Answer_Selection:
		return s.GetSelection().String()
	case *agentsv1.Answer_Input:
		return s.GetInput().String()
	default:
		return ""
	}
}

func (c *ClientContext) Get() string {
	var ss []string
	for i, s := range c.states {
		ss = append(ss, fmt.Sprintf("%d: %s", i, StateAsString(s)))
	}
	return strings.Join(ss, " -> ")
}

type NoOpClientContext struct{}

func (c *NoOpClientContext) Process(*agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return &agentsv1.InformationRequest{Done: true}, nil
}

var _ Client = &NoOpClientContext{}

func NewNoOpClientContext() *NoOpClientContext {
	return &NoOpClientContext{}
}

func (c *NoOpClientContext) Ready() bool {
	return true
}

// Dispatches the request to the appropriate client

type ClientManager struct {
	clients map[agentsv1.Method]*ClientContext
	logger  shared.BaseLogger
}

func (m *ClientManager) WithLogger(logger shared.BaseLogger) {
	m.logger = logger
}

func (m *ClientManager) Add(method agentsv1.Method, client *ClientContext) error {
	m.clients[method] = client
	return nil
}

func (m *ClientManager) Process(eng *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if client, ok := m.clients[eng.Method]; ok {
		m.logger.Debugf("found client context for %v", eng.Method)
		return client.Process(eng)
	}
	return &agentsv1.InformationRequest{}, fmt.Errorf("no client for method: %v", eng.Method)
}

func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[agentsv1.Method]*ClientContext),
	}
}
