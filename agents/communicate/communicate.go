package communicate

import (
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
)

// Factory

const (
	Create = agentsv1.Method_CREATE
)

// Runtime

const (
	Sync = agentsv1.Method_SYNC
)

func (c *ClientContext) Display(msg *agentsv1.Message, data map[string]string) *agentsv1.Question {
	return &agentsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &agentsv1.Question_Display{
			Display: &agentsv1.Display{Data: data},
		},
	}
}

func (c *ClientContext) NewConfirm(msg *agentsv1.Message, defaultConfirm bool) *agentsv1.Question {
	return &agentsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &agentsv1.Question_Confirm{
			Confirm: &agentsv1.Confirm{
				Default: defaultConfirm,
			},
		},
	}
}

func (c *ClientContext) NewStringInput(msg *agentsv1.Message, defaultValue string) *agentsv1.Question {
	return &agentsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &agentsv1.Question_Input{
			Input: &agentsv1.Input{
				Default: &agentsv1.Input_StringDefault{
					StringDefault: defaultValue,
				},
			},
		},
	}
}

func (c *ClientContext) NewSelection(msg *agentsv1.Message, options ...*agentsv1.Message) *agentsv1.Question {
	return &agentsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &agentsv1.Question_Selection{
			Selection: &agentsv1.Selection{
				Options: options,
			},
		},
	}
}

func (c *ClientContext) NewChoice(msg *agentsv1.Message, options ...*agentsv1.Message) *agentsv1.Question {
	return &agentsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &agentsv1.Question_Choice{
			Choice: &agentsv1.Choice{
				Options: options,
			},
		},
	}
}
