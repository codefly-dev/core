package communicate

import (
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
)

func Display(msg *agentv0.Message, data map[string]string) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Display{
			Display: &agentv0.Display{Data: data},
		},
	}
}

func NewConfirm(msg *agentv0.Message, defaultConfirm bool) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Confirm{
			Confirm: &agentv0.Confirm{
				Default: defaultConfirm,
			},
		},
	}
}

func NewStringInput(msg *agentv0.Message, defaultValue string) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Input{
			Input: &agentv0.Input{
				Default: &agentv0.Input_StringDefault{
					StringDefault: defaultValue,
				},
			},
		},
	}
}

func NewSelection(msg *agentv0.Message, options ...*agentv0.Message) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Selection{
			Selection: &agentv0.Selection{
				Options: options,
			},
		},
	}
}

func NewChoice(msg *agentv0.Message, options ...*agentv0.Message) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Choice{
			Choice: &agentv0.Choice{
				Options: options,
			},
		},
	}
}
