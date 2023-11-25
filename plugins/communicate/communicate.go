package communicate

import (
	pluginsv1 "github.com/codefly-dev/core/proto/v1/go/plugins"
)

// Factory

const (
	Create = pluginsv1.Method_CREATE
)

// Runtime

const (
	Sync = pluginsv1.Method_SYNC
)

func (c *ClientContext) Display(msg *pluginsv1.Message, data map[string]string) *pluginsv1.Question {
	return &pluginsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &pluginsv1.Question_Display{
			Display: &pluginsv1.Display{Data: data},
		},
	}
}

func (c *ClientContext) NewConfirm(msg *pluginsv1.Message, defaultConfirm bool) *pluginsv1.Question {
	return &pluginsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &pluginsv1.Question_Confirm{
			Confirm: &pluginsv1.Confirm{
				Default: defaultConfirm,
			},
		},
	}
}

func (c *ClientContext) NewStringInput(msg *pluginsv1.Message, defaultValue string) *pluginsv1.Question {
	return &pluginsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &pluginsv1.Question_Input{
			Input: &pluginsv1.Input{
				Default: &pluginsv1.Input_StringDefault{
					StringDefault: defaultValue,
				},
			},
		},
	}
}

func (c *ClientContext) NewSelection(msg *pluginsv1.Message, options ...*pluginsv1.Message) *pluginsv1.Question {
	return &pluginsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &pluginsv1.Question_Selection{
			Selection: &pluginsv1.Selection{
				Options: options,
			},
		},
	}
}

func (c *ClientContext) NewChoice(msg *pluginsv1.Message, options ...*pluginsv1.Message) *pluginsv1.Question {
	return &pluginsv1.Question{
		Method:  c.Method,
		Round:   c.NextRound(),
		Message: msg,
		Value: &pluginsv1.Question_Choice{
			Choice: &pluginsv1.Choice{
				Options: options,
			},
		},
	}
}
