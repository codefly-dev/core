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
	// following is true when the log pane is pinned to the latest output.
	// When false the user has scrolled up, so we surface how to get back.
	following bool
	// tally is the per-service summary ("4/6 ready · vault slow") shown on the
	// left of the bar in place of the single service name once a multi-service
	// plan is known. Empty for single-service runs (the old behaviour).
	tally string
}

type tickMsg time.Time

func NewStatusBar(service string) StatusBar {
	return StatusBar{
		service:   service,
		state:     StateLoading,
		startedAt: time.Now(),
		following: true,
	}
}

func (b *StatusBar) SetState(state ServiceState) {
	b.state = state
}

// SetTally sets the per-service summary shown on the left of the bar. When set
// it replaces the single service/state label with the aggregate view.
func (b *StatusBar) SetTally(tally string) {
	b.tally = tally
}

// SetFollowing records whether the log pane is pinned to the latest output.
func (b *StatusBar) SetFollowing(following bool) {
	b.following = following
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

	// With a multi-service plan, the left side shows the aggregate tally
	// ("4/6 ready · vault slow"); otherwise it falls back to the single
	// service name + state (single-service runs, tests).
	left := fmt.Sprintf("%s  %s",
		s.Service.Render(b.service),
		s.Muted.Render(b.state.String()))
	if b.tally != "" {
		left = s.Muted.Render(b.tally)
	}
	// When the user has scrolled up, the pane is no longer tailing — tell them
	// how to resume, and flag that newer output exists below.
	hint := "↑↓ scroll"
	if !b.following {
		hint = "↓ more · End to follow"
	}
	right := fmt.Sprintf("%s  %s  Ctrl+C to stop", elapsed, s.Muted.Render(hint))

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
