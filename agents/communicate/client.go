package communicate

import (
	"context"
	"fmt"

	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/wool"
)

// See README.md for more information

func Do[T any](ctx context.Context, agent Communicate, handler AnswerProvider) error {
	w := wool.Get(ctx).In("communicate.Do")
	session := NewClientSession(Channel[T](), handler)
	var req *agentv1.InformationRequest
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
		if eng.Mode == agentv1.Engage_END {
			break
		}
	}
	return nil
}

// Dispatches the request to the appropriate generator

type ClientSession struct {
	channel        *agentv1.Channel
	answerProvider AnswerProvider
}

func (s *ClientSession) Engage(ctx context.Context, req *agentv1.InformationRequest) (*agentv1.Engage, error) {
	w := wool.Get(ctx).In("communicate.ClientSession.Engage")
	if req == nil {
		return &agentv1.Engage{Channel: s.channel, Mode: agentv1.Engage_START}, nil
	}
	// if we don't have a question, we are done
	if req.Question == nil {
		return &agentv1.Engage{Channel: s.channel, Mode: agentv1.Engage_END}, nil
	}
	answer, err := s.answerProvider.Answer(ctx, req.Question)
	if err != nil {
		return nil, w.Wrapf(err, "error answering question")
	}

	return &agentv1.Engage{Channel: s.channel, Stage: req.Question.Message.Name, Answer: answer}, nil
}

type AnswerProvider interface {
	Answer(ctx context.Context, question *agentv1.Question) (*agentv1.Answer, error)
}

func NewClientSession(channel *agentv1.Channel, handler AnswerProvider) *ClientSession {
	return &ClientSession{channel: channel, answerProvider: handler}
}
