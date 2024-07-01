package communicate

import (
	"context"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// A Sequence is a list of total_steps that are executed in order

type Sequence struct {
	step       int
	totalSteps int
	questions  []*agentv0.Question
}

var _ QuestionGenerator = &Sequence{}

func (s *Sequence) Ready() bool {
	return s.step == s.totalSteps
}

func (s *Sequence) Process(_ context.Context, _ *agentv0.Engage) (*agentv0.InformationRequest, error) {
	// We may be done
	if s.step == s.totalSteps {
		return &agentv0.InformationRequest{Done: true}, nil
	}
	// Return the questions in order
	step := s.step
	s.step++
	return &agentv0.InformationRequest{
		Question: s.questions[step],
	}, nil
}

func NewSequence(qs ...*agentv0.Question) *Sequence {
	return &Sequence{
		step:       0,
		totalSteps: len(qs),
		questions:  qs,
	}
}
