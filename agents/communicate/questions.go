package communicate

import (
	agentsv1 "github.com/codefly-dev/core/generated/v1/go/proto/agents"
)

func Display(msg *agentsv1.Message, data map[string]string) *agentsv1.Question {
	return &agentsv1.Question{
		Message: msg,
		Value: &agentsv1.Question_Display{
			Display: &agentsv1.Display{Data: data},
		},
	}
}

func NewConfirm(msg *agentsv1.Message, defaultConfirm bool) *agentsv1.Question {
	return &agentsv1.Question{
		Message: msg,
		Value: &agentsv1.Question_Confirm{
			Confirm: &agentsv1.Confirm{
				Default: defaultConfirm,
			},
		},
	}
}

func NewStringInput(msg *agentsv1.Message, defaultValue string) *agentsv1.Question {
	return &agentsv1.Question{
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

func NewSelection(msg *agentsv1.Message, options ...*agentsv1.Message) *agentsv1.Question {
	return &agentsv1.Question{
		Message: msg,
		Value: &agentsv1.Question_Selection{
			Selection: &agentsv1.Selection{
				Options: options,
			},
		},
	}
}

func NewChoice(msg *agentsv1.Message, options ...*agentsv1.Message) *agentsv1.Question {
	return &agentsv1.Question{
		Message: msg,
		Value: &agentsv1.Question_Choice{
			Choice: &agentsv1.Choice{
				Options: options,
			},
		},
	}
}
