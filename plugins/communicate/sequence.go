package communicate

import (
	pluginsv1 "github.com/codefly-dev/core/proto/v1/go/plugins"
)

// A Sequence is a list of total_steps that are executed in order

type Sequence struct {
	step         int
	totalSteps   int
	questions    []*pluginsv1.Question
	Method       pluginsv1.Method
	namesToSteps map[string]int
}

func (s *Sequence) Ready() bool {
	return s.step == s.totalSteps
}

func (s *Sequence) Find(name string) int {
	return s.namesToSteps[name]
}

func (s *Sequence) Process(req *pluginsv1.Engage) (*pluginsv1.InformationRequest, error) {
	// We may be done
	if s.step == s.totalSteps {
		return &pluginsv1.InformationRequest{Method: s.Method, Done: true}, nil
	}
	// Return the questions in order
	step := s.step
	s.step++
	return &pluginsv1.InformationRequest{
		Method:   s.Method,
		Question: s.questions[step],
	}, nil
}

func NewSequence(method pluginsv1.Method, qs ...*pluginsv1.Question) *Sequence {
	namesToSteps := make(map[string]int)
	for i, q := range qs {
		namesToSteps[q.Message.Name] = i
	}

	return &Sequence{
		step:         0,
		totalSteps:   len(qs),
		questions:    qs,
		namesToSteps: namesToSteps,
	}
}
