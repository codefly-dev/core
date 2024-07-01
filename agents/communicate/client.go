package communicate

import (
	"context"
	"fmt"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/wool"
)

// See README.md for more information

func Do[T any](ctx context.Context, agent Communicate, handler AnswerProvider) error {
	w := wool.Get(ctx).In("communicate.Do")
	session := NewClientSession(Channel[T](), handler)
	var req *agentv0.InformationRequest
	for {
		// client provides the data
		eng, err := session.Engage(ctx, req)
		if err != nil {
			return w.Wrapf(err, "error engaging")
		}

		w.Trace(fmt.Sprintf("sending to agent: %s", eng))
		req, err = agent.Communicate(ctx, eng)
		if err != nil {
			return w.Wrapf(err, "error communicating")
		}
		if eng.Mode == agentv0.Engage_END {
			break
		}
	}
	return nil
}

// Dispatches the request to the appropriate generator

type ClientSession struct {
	channel        *agentv0.Channel
	answerProvider AnswerProvider
}

func (s *ClientSession) Engage(ctx context.Context, req *agentv0.InformationRequest) (*agentv0.Engage, error) {
	w := wool.Get(ctx).In("communicate.ClientSession.Engage")
	if req == nil {
		return &agentv0.Engage{Channel: s.channel, Mode: agentv0.Engage_START}, nil
	}
	// if we don't have a question, we are done
	if req.Question == nil {
		return &agentv0.Engage{Channel: s.channel, Mode: agentv0.Engage_END}, nil
	}
	answer, err := s.answerProvider.Answer(ctx, req.Question)
	if err != nil {
		return nil, w.Wrapf(err, "error answering question")
	}

	return &agentv0.Engage{Channel: s.channel, Stage: req.Question.Message.Name, Answer: answer}, nil
}

type AnswerProvider interface {
	Answer(ctx context.Context, question *agentv0.Question) (*agentv0.Answer, error)
}

func NewClientSession(channel *agentv0.Channel, handler AnswerProvider) *ClientSession {
	return &ClientSession{channel: channel, answerProvider: handler}
}
