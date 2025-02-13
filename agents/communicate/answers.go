package communicate

import (
	"fmt"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// Default values

func GetDefaultConfirm(options []*agentv0.Question, name string) (bool, error) {
	for _, opt := range options {
		if opt.Message.Name == name {
			// check Oneof is confirm
			if confirm, ok := opt.Value.(*agentv0.Question_Confirm); ok {
				return confirm.Confirm.Default, nil
			}
			return false, fmt.Errorf("wrong type in %s for %T", name, opt.Value)
		}
	}
	return false, fmt.Errorf("confirm %s not found", name)
}

func GetDefaultStringInput(options []*agentv0.Question, name string) (string, error) {
	for _, opt := range options {
		if opt.Message.Name == name {
			// check Oneof is confirm
			if input, ok := opt.Value.(*agentv0.Question_Input); ok {
				if s, ok := input.Input.Default.(*agentv0.Input_StringDefault); ok {
					return s.StringDefault, nil
				}
			}
			return "", fmt.Errorf("wrong type in %s for %T", name, opt.Value)
		}
	}
	return "", fmt.Errorf("confirm %s not found", name)
}

func GetDefaultChoice(options []*agentv0.Question, name string) (string, error) {
	// For now returns the first option
	for _, opt := range options {
		if opt.Message.Name == name {
			if choice, ok := opt.Value.(*agentv0.Question_Choice); ok {
				return choice.Choice.Options[0].Name, nil
			}
			return "", fmt.Errorf("wrong type in %s for %T", name, opt.Value)
		}
	}
	return "", fmt.Errorf("choice %s not found", name)
}

// Sessions

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

func (session *ServerSession) GetIntString(stage string) (int, error) {
	answer, err := session.Input(stage)
	if err != nil {
		return 0, fmt.Errorf("cannot find stage %s", stage)
	}
	return int(answer.GetIntValue()), nil
}

func (session *ServerSession) GetChoice(stage string) (string, error) {
	answer := session.states[stage]
	if answer == nil {
		return "", fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetChoice().Option, nil
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
