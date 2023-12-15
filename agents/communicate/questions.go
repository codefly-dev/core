package communicate

import (
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
)

func Display(msg *agentv1.Message, data map[string]string) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Display{
			Display: &agentv1.Display{Data: data},
		},
	}
}

func NewConfirm(msg *agentv1.Message, defaultConfirm bool) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Confirm{
			Confirm: &agentv1.Confirm{
				Default: defaultConfirm,
			},
		},
	}
}

func NewStringInput(msg *agentv1.Message, defaultValue string) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Input{
			Input: &agentv1.Input{
				Default: &agentv1.Input_StringDefault{
					StringDefault: defaultValue,
				},
			},
		},
	}
}

func NewSelection(msg *agentv1.Message, options ...*agentv1.Message) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Selection{
			Selection: &agentv1.Selection{
				Options: options,
			},
		},
	}
}

func NewChoice(msg *agentv1.Message, options ...*agentv1.Message) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Choice{
			Choice: &agentv1.Choice{
				Options: options,
			},
		},
	}
}
