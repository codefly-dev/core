package communicate

import (
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

func TestChoiceAndSelectionDefaultsUseStableOptionNames(t *testing.T) {
	message := &agentv0.Message{Name: "runtime"}
	development := &agentv0.Message{Name: "development", Message: "Development"}
	production := &agentv0.Message{Name: "production", Message: "Production"}

	choice := NewChoiceWithDefault(message, "development", development, production)
	require.Equal(t, "development", choice.GetChoice().GetDefaultOption())
	require.Equal(t, []*agentv0.Message{development, production}, choice.GetChoice().GetOptions())

	selection := NewSelectionWithDefault(message, []string{"production"}, development, production)
	require.Equal(t, []string{"production"}, selection.GetSelection().GetDefault().GetOptions())
	require.Equal(t, []*agentv0.Message{development, production}, selection.GetSelection().GetOptions())

	empty := NewSelectionWithDefault(message, nil, development, production)
	require.NotNil(t, empty.GetSelection().GetDefault())
	require.Empty(t, empty.GetSelection().GetDefault().GetOptions())
	require.Nil(t, NewSelection(message, development, production).GetSelection().GetDefault())
}
