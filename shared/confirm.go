package shared

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/codefly-dev/golor"
)

type ConfirmModel struct {
	confirmed bool
	Prompt    string
}

func (m ConfirmModel) Init() tea.Cmd {
	return nil
}

func (m ConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle Ctrl+C and Ctrl+D
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlD {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter {
			m.confirmed = true
			return m, tea.Quit
		}
		switch msg.String() {
		case "y":
			m.confirmed = true
			return m, tea.Quit
		case "n", "q", "esc":
			m.confirmed = false
			return m, tea.Quit

		}
	}
	return m, nil
}

func (m ConfirmModel) View() string {
	// Render a block of text.
	style := lipgloss.NewStyle().
		Margin(1, 2, 1, 2)
	return style.Render(golor.Sprintf("#(bold,magenta)[{{.Prompt}}] (Y/n)", m))
}

func Confirm(s string) bool {
	// Catch interrupt signal

	p := tea.NewProgram(ConfirmModel{
		Prompt: s,
	})
	mod, err := p.Run()
	if err != nil {
		return false
	}
	m := mod.(ConfirmModel)
	return m.confirmed
}
