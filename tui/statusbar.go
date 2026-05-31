package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// StatusBar renders a bottom bar with service name, state, and elapsed time.
type StatusBar struct {
	service   string
	state     ServiceState
	startedAt time.Time
	width     int
}

type tickMsg time.Time

func NewStatusBar(service string) StatusBar {
	return StatusBar{
		service:   service,
		state:     StateLoading,
		startedAt: time.Now(),
	}
}

func (b *StatusBar) SetState(state ServiceState) {
	b.state = state
}

func (b *StatusBar) SetWidth(w int) {
	b.width = w
}

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (b *StatusBar) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case tickMsg:
		return tickEvery()
	}
	return nil
}

func (b *StatusBar) View() string {
	s := Styles()
	elapsed := time.Since(b.startedAt).Truncate(time.Second)

	left := fmt.Sprintf("%s  %s",
		s.Service.Render(b.service),
		s.Muted.Render(b.state.String()))
	right := fmt.Sprintf("%s  Ctrl+C to stop", elapsed)

	// StatusBar style applies Padding(0, 1), which adds one column on
	// each side. Lay the content out against the inner width so the
	// rendered bar (content + padding) is exactly b.width and the
	// right-hand "Ctrl+C to stop" never wraps onto the next line.
	inner := b.width - 2
	gap := inner - len(stripAnsi(left)) - len(stripAnsi(right))
	if gap < 0 {
		gap = 0
	}

	return s.StatusBar.Render(left + strings.Repeat(" ", gap) + right)
}

func stripAnsi(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
