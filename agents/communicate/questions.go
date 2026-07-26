package communicate

import (
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
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

func NewIntInput(msg *agentv0.Message, defaultValue int) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Input{
			Input: &agentv0.Input{
				Default: &agentv0.Input_IntDefault{
					IntDefault: int32(defaultValue),
				},
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

// NewChoiceWithDefault declares the deterministic option selected by
// non-interactive callers. defaultOption is the stable Message.Name, not its
// display label.
func NewChoiceWithDefault(msg *agentv0.Message, defaultOption string, options ...*agentv0.Message) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Choice{
			Choice: &agentv0.Choice{
				Options:       options,
				DefaultOption: defaultOption,
			},
		},
	}
}

// NewSelectionWithDefault declares the deterministic set selected by
// non-interactive callers. Each value is a stable Message.Name.
func NewSelectionWithDefault(msg *agentv0.Message, defaultOptions []string, options ...*agentv0.Message) *agentv0.Question {
	return &agentv0.Question{
		Message: msg,
		Value: &agentv0.Question_Selection{
			Selection: &agentv0.Selection{
				Options: options,
				Default: &agentv0.SelectionDefault{
					Options: append([]string(nil), defaultOptions...),
				},
			},
		},
	}
}
