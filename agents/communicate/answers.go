package communicate

import (
	"fmt"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// AnswerDefault resolves a question using only its protocol-declared default.
// It is the shared non-interactive policy for CI, MCP, pipes, and orchestration:
// callers must never guess a choice that the plugin did not declare.
func AnswerDefault(question *agentv0.Question) (*agentv0.Answer, error) {
	if question == nil {
		return nil, fmt.Errorf("cannot answer nil question")
	}
	name := "<unnamed>"
	if question.Message != nil && question.Message.Name != "" {
		name = question.Message.Name
	}
	switch value := question.Value.(type) {
	case *agentv0.Question_Confirm:
		if value.Confirm == nil {
			return nil, fmt.Errorf("headless question %q has no confirm definition", name)
		}
		return &agentv0.Answer{
			Value: &agentv0.Answer_Confirm{
				Confirm: &agentv0.ConfirmAnswer{Confirmed: value.Confirm.GetDefault()},
			},
		}, nil
	case *agentv0.Question_Input:
		if value.Input == nil || value.Input.Default == nil {
			return nil, fmt.Errorf("headless question %q requires input but declares no default", name)
		}
		switch defaultValue := value.Input.Default.(type) {
		case *agentv0.Input_StringDefault:
			return &agentv0.Answer{
				Value: &agentv0.Answer_Input{
					Input: &agentv0.InputAnswer{
						Answer: &agentv0.InputAnswer_StringValue{StringValue: defaultValue.StringDefault},
					},
				},
			}, nil
		case *agentv0.Input_IntDefault:
			return &agentv0.Answer{
				Value: &agentv0.Answer_Input{
					Input: &agentv0.InputAnswer{
						Answer: &agentv0.InputAnswer_IntValue{IntValue: defaultValue.IntDefault},
					},
				},
			}, nil
		default:
			return nil, fmt.Errorf("headless question %q has an unsupported input default %T", name, defaultValue)
		}
	case *agentv0.Question_Choice:
		if value.Choice == nil || value.Choice.GetDefaultOption() == "" {
			return nil, fmt.Errorf("headless question %q requires a choice but declares no default option", name)
		}
		defaultOption := value.Choice.GetDefaultOption()
		if !containsOption(value.Choice.GetOptions(), defaultOption) {
			return nil, fmt.Errorf("headless question %q declares unknown default option %q", name, defaultOption)
		}
		return &agentv0.Answer{
			Value: &agentv0.Answer_Choice{
				Choice: &agentv0.ChoiceAnswer{Option: defaultOption},
			},
		}, nil
	case *agentv0.Question_Selection:
		if value.Selection == nil || value.Selection.GetDefault() == nil {
			return nil, fmt.Errorf("headless question %q requires a selection but declares no default selection", name)
		}
		selected := append([]string(nil), value.Selection.GetDefault().GetOptions()...)
		seen := make(map[string]struct{}, len(selected))
		for _, option := range selected {
			if !containsOption(value.Selection.GetOptions(), option) {
				return nil, fmt.Errorf("headless question %q declares unknown default selection %q", name, option)
			}
			if _, duplicate := seen[option]; duplicate {
				return nil, fmt.Errorf("headless question %q declares duplicate default selection %q", name, option)
			}
			seen[option] = struct{}{}
		}
		return &agentv0.Answer{
			Value: &agentv0.Answer_Selection{
				Selection: &agentv0.SelectionAnswer{Selected: selected},
			},
		}, nil
	default:
		return nil, fmt.Errorf("headless question %q has no protocol-declared default for %T", name, question.Value)
	}
}

func containsOption(options []*agentv0.Message, name string) bool {
	for _, option := range options {
		if option != nil && option.Name == name {
			return true
		}
	}
	return false
}

// Default value extractors from question definitions.

func GetDefaultConfirm(options []*agentv0.Question, name string) (bool, error) {
	for _, opt := range options {
		if opt.Message.Name == name {
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
			if input, ok := opt.Value.(*agentv0.Question_Input); ok {
				if s, ok := input.Input.Default.(*agentv0.Input_StringDefault); ok {
					return s.StringDefault, nil
				}
			}
			return "", fmt.Errorf("wrong type in %s for %T", name, opt.Value)
		}
	}
	return "", fmt.Errorf("input %s not found", name)
}

// Answer extractors from a map of answers (returned by QuestionAsker.RunSequence).

func Confirm(answers map[string]*agentv0.Answer, stage string) (bool, error) {
	answer, ok := answers[stage]
	if !ok || answer == nil {
		return false, fmt.Errorf("cannot find answer for %s", stage)
	}
	// GetConfirm() returns nil if the answer holds a different oneof variant;
	// a raw `.Confirmed` on that nil pointer would panic the asker.
	c := answer.GetConfirm()
	if c == nil {
		return false, fmt.Errorf("answer for %s is not a confirm answer", stage)
	}
	return c.Confirmed, nil
}

func Selection(answers map[string]*agentv0.Answer, stage string) (*agentv0.SelectionAnswer, error) {
	answer, ok := answers[stage]
	if !ok || answer == nil {
		return nil, fmt.Errorf("cannot find answer for %s", stage)
	}
	return answer.GetSelection(), nil
}

func InputString(answers map[string]*agentv0.Answer, stage string) (string, error) {
	answer, ok := answers[stage]
	if !ok || answer == nil {
		return "", fmt.Errorf("cannot find answer for %s", stage)
	}
	return answer.GetInput().GetStringValue(), nil
}

func InputInt(answers map[string]*agentv0.Answer, stage string) (int, error) {
	answer, ok := answers[stage]
	if !ok || answer == nil {
		return 0, fmt.Errorf("cannot find answer for %s", stage)
	}
	return int(answer.GetInput().GetIntValue()), nil
}

func Choice(answers map[string]*agentv0.Answer, stage string) (*agentv0.ChoiceAnswer, error) {
	answer, ok := answers[stage]
	if !ok || answer == nil {
		return nil, fmt.Errorf("cannot find answer for %s", stage)
	}
	return answer.GetChoice(), nil
}
