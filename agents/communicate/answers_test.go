package communicate

import (
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

func TestAnswerDefaultResolvesDeclaredChoiceAndSelection(t *testing.T) {
	options := []*agentv0.Message{
		{Name: "development"},
		{Name: "production"},
	}

	choice, err := AnswerDefault(NewChoiceWithDefault(
		&agentv0.Message{Name: "runtime"},
		"production",
		options...,
	))
	require.NoError(t, err)
	require.Equal(t, "production", choice.GetChoice().GetOption())

	selection, err := AnswerDefault(NewSelectionWithDefault(
		&agentv0.Message{Name: "targets"},
		[]string{"development", "production"},
		options...,
	))
	require.NoError(t, err)
	require.Equal(t, []string{"development", "production"}, selection.GetSelection().GetSelected())

	empty, err := AnswerDefault(NewSelectionWithDefault(
		&agentv0.Message{Name: "targets"},
		nil,
		options...,
	))
	require.NoError(t, err)
	require.Empty(t, empty.GetSelection().GetSelected())
}

func TestAnswerDefaultRejectsMissingOrInvalidDeclaredDecisions(t *testing.T) {
	option := &agentv0.Message{Name: "development"}
	tests := []*agentv0.Question{
		NewChoice(&agentv0.Message{Name: "runtime"}, option),
		NewChoiceWithDefault(&agentv0.Message{Name: "runtime"}, "unknown", option),
		NewSelection(&agentv0.Message{Name: "targets"}, option),
		NewSelectionWithDefault(&agentv0.Message{Name: "targets"}, []string{"unknown"}, option),
		NewSelectionWithDefault(&agentv0.Message{Name: "targets"}, []string{"development", "development"}, option),
	}
	for _, question := range tests {
		_, err := AnswerDefault(question)
		require.Error(t, err)
		require.Contains(t, err.Error(), "headless question")
	}
}
