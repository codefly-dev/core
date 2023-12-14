package communicate

import (
	"context"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	"github.com/codefly-dev/core/shared"
)

// See README.md for more information

func Do[T any](ctx context.Context, agent Communicate, handler AnswerProvider) error {
	logger := shared.GetLogger(ctx).With("communicate.Communicate<%s>", shared.TypeOf[T]())
	logger.DebugMe("Starting communication")
	session := NewClientSession(Channel[T](), handler)
	var req *agentsv1.InformationRequest
	for {
		// client provides the data
		eng, err := session.Engage(ctx, req)
		logger.DebugMe("Creating engagement: %s", req)
		if err != nil {
			return logger.Wrapf(err, "error engaging")
		}

		req, err = agent.Communicate(ctx, eng)
		logger.DebugMe("Received request: %s", req)
		if eng.Mode == agentsv1.Engage_END {
			logger.DebugMe("Communication ended")
			break
		}
	}
	return nil
}

// Dispatches the request to the appropriate generator

type ClientSession struct {
	channel        *agentsv1.Channel
	answerProvider AnswerProvider
}

func (s *ClientSession) Engage(ctx context.Context, req *agentsv1.InformationRequest) (*agentsv1.Engage, error) {
	logger := shared.GetLogger(ctx).With("communicate.ClientSession.Engage")
	if req == nil {
		return &agentsv1.Engage{Channel: s.channel, Mode: agentsv1.Engage_START}, nil
	}
	// if we don't have a question, we are done
	if req.Question == nil {
		return &agentsv1.Engage{Channel: s.channel, Mode: agentsv1.Engage_END}, nil
	}
	answer, err := s.answerProvider.Answer(ctx, req.Question)
	if err != nil {
		return nil, logger.Wrapf(err, "error answering question")
	}

	return &agentsv1.Engage{Channel: s.channel, Stage: req.Question.Message.Name, Answer: answer}, nil
}

type AnswerProvider interface {
	Answer(ctx context.Context, question *agentsv1.Question) (*agentsv1.Answer, error)
}

func NewClientSession(channel *agentsv1.Channel, handler AnswerProvider) *ClientSession {
	return &ClientSession{channel: channel, answerProvider: handler}
}
