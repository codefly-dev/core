package communicate

import (
	"fmt"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
)

func (session *ServerSession) Confirm(stage string) (bool, error) {
	answer := session.states[stage]
	if answer == nil {
		return false, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetConfirm().Confirmed, nil
}

func (session *ServerSession) Selection(stage string) (*agentv0.SelectionAnswer, error) {
	answer := session.states[stage]
	if answer == nil {
		return nil, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetSelection(), nil
}

func (session *ServerSession) Input(stage string) (*agentv0.InputAnswer, error) {
	answer := session.states[stage]
	if answer == nil {
		return nil, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetInput(), nil
}

func (session *ServerSession) GetInputString(stage string) (string, error) {
	answer, err := session.Input(stage)
	if err != nil {
		return "", fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetStringValue(), nil
}

func (session *ServerSession) Choice(stage string) (*agentv0.ChoiceAnswer, error) {
	answer := session.states[stage]
	if answer == nil {
		return nil, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetChoice(), nil
}

func StateAsString(s *agentv0.Answer) string {
	switch s.Value.(type) {
	case *agentv0.Answer_Confirm:
		return s.GetConfirm().String()
	case *agentv0.Answer_Selection:
		return s.GetSelection().String()
	case *agentv0.Answer_Input:
		return s.GetInput().String()
	case *agentv0.Answer_Choice:
		return s.GetChoice().String()
	default:
		return ""
	}
}

func (session *ServerSession) String() string {
	var ss []string
	for i, s := range session.states {
		ss = append(ss, fmt.Sprintf("%s: %s", i, s))
	}
	return strings.Join(ss, " -> ")
}
