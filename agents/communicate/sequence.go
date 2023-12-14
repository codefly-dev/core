package communicate

import (
	"context"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
)

// A Sequence is a list of total_steps that are executed in order

type Sequence struct {
	step       int
	totalSteps int
	questions  []*agentsv1.Question
}

var _ QuestionGenerator = &Sequence{}

func (s *Sequence) Ready() bool {
	return s.step == s.totalSteps
}

func (s *Sequence) Process(ctx context.Context, eng *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	// We may be done
	if s.step == s.totalSteps {
		return &agentsv1.InformationRequest{Done: true}, nil
	}
	// Return the questions in order
	step := s.step
	s.step++
	return &agentsv1.InformationRequest{
		Question: s.questions[step],
	}, nil
}

func NewSequence(qs ...*agentsv1.Question) *Sequence {
	return &Sequence{
		step:       0,
		totalSteps: len(qs),
		questions:  qs,
	}
}
