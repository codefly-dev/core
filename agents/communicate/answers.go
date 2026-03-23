package communicate

import (
	"fmt"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

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
	return answer.GetConfirm().Confirmed, nil
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
