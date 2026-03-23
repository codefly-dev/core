package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Entry represents a selectable item in a choice/select prompt.
type Entry struct {
	Identifier  string
	Current     bool
	Description string
}

func (e *Entry) String() string {
	display := e.Identifier
	if e.Current {
		display += " (active)"
	}
	return Styles().Bold.Render(display)
}

// --- Choice ---

type choiceModel struct {
	message       string
	entries       []*Entry
	cursor        int
	selectedEntry *Entry
	stopped       bool
}

func (m choiceModel) Init() tea.Cmd { return nil }

func (m choiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "j", "down":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			m.selectedEntry = m.entries[m.cursor]
			return m, tea.Quit
		case "ctrl+c":
			m.stopped = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m choiceModel) View() string {
	s := Styles().Header.Render(m.message) + "\n"
	for i, entry := range m.entries {
		cursor := "  "
		if m.cursor == i {
			cursor = Styles().Service.Render("> ")
		}
		s += fmt.Sprintf("%s%s\n", cursor, entry)
	}
	return s
}

// ChoiceResult holds the outcome of a choice prompt.
type ChoiceResult struct {
	Entry   *Entry
	Stopped bool
}

// RunChoice runs an interactive choice prompt. Returns the selected entry
// and whether the user cancelled (Ctrl+C).
func RunChoice(message string, entries []*Entry) (ChoiceResult, error) {
	p := tea.NewProgram(choiceModel{message: message, entries: entries})
	mod, err := p.Run()
	if err != nil {
		return ChoiceResult{}, err
	}
	m := mod.(choiceModel)
	return ChoiceResult{Entry: m.selectedEntry, Stopped: m.stopped}, nil
}

// --- Select ---

type selectModel struct {
	message       string
	entries       []*Entry
	cursor        int
	selectedEntry *Entry
	stopped       bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "j", "down":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			m.selectedEntry = m.entries[m.cursor]
			return m, tea.Quit
		case "ctrl+c":
			m.stopped = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	s := Styles().Header.Render(m.message) + "\n"
	for i, entry := range m.entries {
		cursor := "  "
		if m.cursor == i {
			cursor = Styles().Service.Render("> ")
		}
		s += fmt.Sprintf("%s%s\n", cursor, entry)
	}
	return s
}

// SelectResult holds the outcome of a select prompt.
type SelectResult struct {
	Entry   *Entry
	Stopped bool
}

// RunSelect runs an interactive selection prompt.
func RunSelect(message string, entries []*Entry) (SelectResult, error) {
	p := tea.NewProgram(selectModel{message: message, entries: entries})
	mod, err := p.Run()
	if err != nil {
		return SelectResult{}, err
	}
	m := mod.(selectModel)
	return SelectResult{Entry: m.selectedEntry, Stopped: m.stopped}, nil
}

// --- Confirm ---

type confirmModel struct {
	message   string
	options   string
	confirmed bool
	stopped   bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlD {
			m.stopped = true
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter {
			return m, tea.Quit
		}
		switch msg.String() {
		case "y", "Y":
			m.confirmed = true
			return m, tea.Quit
		case "n", "N", "q", "esc":
			m.confirmed = false
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	style := lipgloss.NewStyle().Margin(1, 2, 1, 2)
	return style.Render(
		Styles().Header.Render(m.message) + " " + Styles().Muted.Render(m.options))
}

// ConfirmResult holds the outcome of a confirm prompt.
type ConfirmResult struct {
	Confirmed bool
	Stopped   bool
}

// DefaultInput returns "(Y/n)" or "(y/N)" based on the default value.
func DefaultInput(def bool) string {
	if def {
		return "(Y/n)"
	}
	return "(y/N)"
}

// RunConfirm runs an interactive yes/no prompt.
func RunConfirm(message string, defaultValue bool) (ConfirmResult, error) {
	p := tea.NewProgram(confirmModel{
		message:   message,
		options:   DefaultInput(defaultValue),
		confirmed: defaultValue,
	})
	mod, err := p.Run()
	if err != nil {
		return ConfirmResult{}, err
	}
	m := mod.(confirmModel)
	return ConfirmResult{Confirmed: m.confirmed, Stopped: m.stopped}, nil
}

// --- Input ---

type inputModel struct {
	message string
	input   string
	stopped bool
}

func (m inputModel) Init() tea.Cmd { return nil }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.Type {
		case tea.KeySpace:
			m.input += " "
		case tea.KeyRunes:
			m.input += string(msg.Runes)
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case tea.KeyEnter:
			return m, tea.Quit
		case tea.KeyCtrlC:
			m.stopped = true
			return m, tea.Quit
		default:
			m.stopped = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m inputModel) View() string {
	return Styles().Header.Render(m.message) + ":\n" + Styles().Bold.Render(m.input)
}

// InputResult holds the outcome of an input prompt.
type InputResult struct {
	Value   string
	Stopped bool
}

// RunInput runs an interactive text input prompt.
func RunInput(message string, defaultValue string) (InputResult, error) {
	p := tea.NewProgram(inputModel{message: message, input: defaultValue})
	mod, err := p.Run()
	if err != nil {
		return InputResult{}, err
	}
	m := mod.(inputModel)
	return InputResult{Value: m.input, Stopped: m.stopped}, nil
}

// --- Paginate ---

type paginateModel struct {
	items     []string
	paginator paginator.Model
}

func (m paginateModel) Init() tea.Cmd { return nil }

func (m paginateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if msg, ok := msg.(tea.KeyMsg); ok {
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEnter {
			return m, tea.Quit
		}
		if msg.String() == "q" {
			return m, tea.Quit
		}
	}
	m.paginator, cmd = m.paginator.Update(msg)
	return m, cmd
}

func (m paginateModel) View() string {
	var b strings.Builder
	start, end := m.paginator.GetSliceBounds(len(m.items))
	for _, item := range m.items[start:end] {
		b.WriteString("  • " + item + "\n\n")
	}
	b.WriteString("  " + m.paginator.View())
	b.WriteString("\n  h/l ←/→ page • q/Enter: done\n")
	return b.String()
}

// RunPaginate shows text in a paginated view. Blocks until dismissed.
func RunPaginate(text string) error {
	items := strings.Split(text, "\n")
	p := paginator.New()
	p.Type = paginator.Dots
	p.PerPage = 10
	p.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	p.SetTotalPages(len(items))

	prog := tea.NewProgram(paginateModel{items: items, paginator: p})
	_, err := prog.Run()
	return err
}
