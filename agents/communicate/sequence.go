package communicate

import (
	"context"

	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
)

// A Sequence is a list of total_steps that are executed in order

type Sequence struct {
	step       int
	totalSteps int
	questions  []*agentv1.Question
}

var _ QuestionGenerator = &Sequence{}

func (s *Sequence) Ready() bool {
	return s.step == s.totalSteps
}

func (s *Sequence) Process(_ context.Context, _ *agentv1.Engage) (*agentv1.InformationRequest, error) {
	// We may be done
	if s.step == s.totalSteps {
		return &agentv1.InformationRequest{Done: true}, nil
	}
	// Return the questions in order
	step := s.step
	s.step++
	return &agentv1.InformationRequest{
		Question: s.questions[step],
	}, nil
}

func NewSequence(qs ...*agentv1.Question) *Sequence {
	return &Sequence{
		step:       0,
		totalSteps: len(qs),
		questions:  qs,
	}
}
