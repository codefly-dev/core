package communicate

import (
	"context"

	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/shared"
)

// See README.md for more information

func Do[T any](ctx context.Context, agent Communicate, handler AnswerProvider) error {
	logger := shared.GetLogger(ctx).With("communicate.Communicate<%s>", shared.TypeOf[T]())
	logger.DebugMe("Starting communication")
	session := NewClientSession(Channel[T](), handler)
	var req *agentv1.InformationRequest
	for {
		// client provides the data
		eng, err := session.Engage(ctx, req)
		logger.DebugMe("Creating engagement: %s", req)
		if err != nil {
			return logger.Wrapf(err, "error engaging")
		}

		req, err = agent.Communicate(ctx, eng)
		if err != nil {
			return logger.Wrapf(err, "error communicating")
		}
		logger.DebugMe("Received request: %s", req)
		if eng.Mode == agentv1.Engage_END {
			logger.DebugMe("Communication ended")
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
	logger := shared.GetLogger(ctx).With("communicate.ClientSession.Engage")
	if req == nil {
		return &agentv1.Engage{Channel: s.channel, Mode: agentv1.Engage_START}, nil
	}
	// if we don't have a question, we are done
	if req.Question == nil {
		return &agentv1.Engage{Channel: s.channel, Mode: agentv1.Engage_END}, nil
	}
	answer, err := s.answerProvider.Answer(ctx, req.Question)
	if err != nil {
		return nil, logger.Wrapf(err, "error answering question")
	}

	return &agentv1.Engage{Channel: s.channel, Stage: req.Question.Message.Name, Answer: answer}, nil
}

type AnswerProvider interface {
	Answer(ctx context.Context, question *agentv1.Question) (*agentv1.Answer, error)
}

func NewClientSession(channel *agentv1.Channel, handler AnswerProvider) *ClientSession {
	return &ClientSession{channel: channel, answerProvider: handler}
}
