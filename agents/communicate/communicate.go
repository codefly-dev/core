package communicate

import (
	"context"
	"io"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/wool"
	"google.golang.org/grpc"
)

// AnswerProvider answers interactive questions from a plugin.
// The CLI implements this (e.g. terminal prompts, defaults).
type AnswerProvider interface {
	Answer(ctx context.Context, question *agentv0.Question) (*agentv0.Answer, error)
}

// QuestionAsker is implemented by plugins to ask questions during
// Create/Sync. It sends Questions and receives Answers on a bidi stream.
type QuestionAsker struct {
	stream grpc.BidiStreamingServer[agentv0.Answer, agentv0.Question]
}

// NewQuestionAsker wraps a bidi stream for the plugin side.
func NewQuestionAsker(stream grpc.BidiStreamingServer[agentv0.Answer, agentv0.Question]) *QuestionAsker {
	return &QuestionAsker{stream: stream}
}

// Ask sends a question to the CLI and waits for the answer.
func (qa *QuestionAsker) Ask(question *agentv0.Question) (*agentv0.Answer, error) {
	if err := qa.stream.Send(question); err != nil {
		return nil, err
	}
	answer, err := qa.stream.Recv()
	if err != nil {
		return nil, err
	}
	return answer, nil
}

// RunSequence asks a sequence of questions and returns all answers keyed by question name.
func (qa *QuestionAsker) RunSequence(questions []*agentv0.Question) (map[string]*agentv0.Answer, error) {
	answers := make(map[string]*agentv0.Answer)
	for _, q := range questions {
		answer, err := qa.Ask(q)
		if err != nil {
			return nil, err
		}
		answers[q.Message.Name] = answer
	}
	return answers, nil
}

// Do drives a bidirectional Communicate stream from the CLI side.
// It reads Questions from the stream, passes them to the AnswerProvider,
// and sends Answers back until the stream closes.
func Do(ctx context.Context, stream grpc.BidiStreamingClient[agentv0.Answer, agentv0.Question], handler AnswerProvider) error {
	w := wool.Get(ctx).In("communicate.Do")
	for {
		question, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return w.Wrapf(err, "receiving question from plugin")
		}

		answer, err := handler.Answer(ctx, question)
		if err != nil {
			return w.Wrapf(err, "answering question")
		}

		if err := stream.Send(answer); err != nil {
			return w.Wrapf(err, "sending answer to plugin")
		}
	}
}
