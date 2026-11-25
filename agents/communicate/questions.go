package communicate

import (
	agentv1 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v1"
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

func NewIntInput(msg *agentv1.Message, defaultValue int) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Input{
			Input: &agentv1.Input{
				Default: &agentv1.Input_IntDefault{
					IntDefault: int32(defaultValue),
				},
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

// NewChoiceWithDefault declares the deterministic option selected by
// non-interactive callers. defaultOption is the stable Message.Name, not its
// display label.
func NewChoiceWithDefault(msg *agentv1.Message, defaultOption string, options ...*agentv1.Message) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Choice{
			Choice: &agentv1.Choice{
				Options:       options,
				DefaultOption: defaultOption,
			},
		},
	}
}

// NewSelectionWithDefault declares the deterministic set selected by
// non-interactive callers. Each value is a stable Message.Name.
func NewSelectionWithDefault(msg *agentv1.Message, defaultOptions []string, options ...*agentv1.Message) *agentv1.Question {
	return &agentv1.Question{
		Message: msg,
		Value: &agentv1.Question_Selection{
			Selection: &agentv1.Selection{
				Options: options,
				Default: &agentv1.SelectionDefault{
					Options: append([]string(nil), defaultOptions...),
				},
			},
		},
	}
}
