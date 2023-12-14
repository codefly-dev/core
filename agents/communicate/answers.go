package communicate

import (
	"fmt"
	"strings"

	agentsv1 "github.com/codefly-dev/core/generated/v1/go/proto/agents"
)

func (c *ServerSession) Confirm(stage string) (bool, error) {
	answer := c.states[stage]
	if answer == nil {
		return false, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetConfirm().Confirmed, nil
}

func (c *ServerSession) Selection(stage string) (*agentsv1.SelectionAnswer, error) {
	answer := c.states[stage]
	if answer == nil {
		return nil, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetSelection(), nil
}

func (c *ServerSession) Input(stage string) (*agentsv1.InputAnswer, error) {
	answer := c.states[stage]
	if answer == nil {
		return nil, fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetInput(), nil
}

func (c *ServerSession) GetInputString(stage string) (string, error) {
	answer, err := c.Input(stage)
	if err != nil {
		return "", fmt.Errorf("cannot find stage %s", stage)
	}
	return answer.GetStringValue(), nil

}

func StateAsString(s *agentsv1.Answer) string {
	switch s.Value.(type) {
	case *agentsv1.Answer_Confirm:
		return s.GetConfirm().String()
	case *agentsv1.Answer_Selection:
		return s.GetSelection().String()
	case *agentsv1.Answer_Input:
		return s.GetInput().String()
	default:
		return ""
	}
}

func (c *ServerSession) String() string {
	var ss []string
	for i, s := range c.states {
		ss = append(ss, fmt.Sprintf("%s: %s", i, s))
	}
	return strings.Join(ss, " -> ")
}
