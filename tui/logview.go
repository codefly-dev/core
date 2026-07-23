package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/codefly-dev/core/wool"
)

// LogView is a scrollable log panel that renders styled log entries.
type LogView struct {
	viewport viewport.Model
	lines    *strings.Builder
	ready    bool
}

// NewLogView creates a LogView with a sensible default size.
func NewLogView() LogView {
	vp := viewport.New(80, 20)
	return LogView{viewport: vp, lines: &strings.Builder{}, ready: true}
}

func (l *LogView) Init() tea.Cmd {
	return nil
}

func (l *LogView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !l.ready {
			l.viewport = viewport.New(msg.Width, msg.Height)
			l.viewport.SetContent(l.lines.String())
			l.ready = true
		} else {
			l.viewport.Width = msg.Width
			l.viewport.Height = msg.Height
		}
	case ServiceLogMsg:
		l.appendLog(msg)
	}
	var cmd tea.Cmd
	l.viewport, cmd = l.viewport.Update(msg)
	return cmd
}

func (l *LogView) appendLog(msg ServiceLogMsg) {
	// Follow the tail only when the user is already at the bottom. If they have
	// scrolled up to read earlier output, leave their position alone instead of
	// yanking them back down on every new line.
	follow := !l.ready || l.viewport.AtBottom()
	for _, line := range formatServiceLogLines(msg, l.viewport.Width) {
		l.lines.WriteString(renderServiceLogLine(msg, line) + "\n")
	}
	if l.ready {
		l.viewport.SetContent(l.lines.String())
		if follow {
			l.viewport.GotoBottom()
		}
	}
}

// AtBottom reports whether the viewport is scrolled to the latest output.
func (l *LogView) AtBottom() bool {
	return !l.ready || l.viewport.AtBottom()
}

func renderServiceLogLine(msg ServiceLogMsg, line string) string {
	s := Styles()
	switch msg.Level {
	case wool.INFO:
		return s.LogInfo.Render(line)
	case wool.WARN:
		return s.LogWarn.Render(line)
	case wool.ERROR:
		return s.LogError.Render(line)
	case wool.DEBUG:
		return s.LogDebug.Render(line)
	case wool.TRACE:
		return s.LogTrace.Render(line)
	case wool.FORWARD:
		if msg.Source != "" {
			return ServiceRenderer(msg.Source)(line)
		}
		return s.LogForward.Render(line)
	default:
		return s.LogForward.Render(line)
	}
}

func formatServiceLogLines(msg ServiceLogMsg, width int) []string {
	source := msg.Source
	if source == "" {
		source = "system"
	}
	separator := "|"
	if msg.Level == wool.FORWARD {
		separator = ">"
	}
	message := strings.ReplaceAll(msg.Message, "\r\n", "\n")
	message = strings.TrimRight(message, "\r\n")
	rawLines := strings.Split(message, "\n")
	prefix := fmt.Sprintf("%s %s ", padServiceSource(source), separator)
	out := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		raw = strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		out = append(out, wrapLogLine(prefix, raw, width)...)
	}
	return out
}

func padServiceSource(source string) string {
	const minWidth = 16
	if len(source) >= minWidth {
		return source
	}
	return fmt.Sprintf("%-*s", minWidth, source)
}

func wrapLogLine(prefix, message string, width int) []string {
	if width <= 0 || len(prefix)+len(message) <= width {
		return []string{prefix + message}
	}
	bodyWidth := width - len(prefix)
	if bodyWidth < 20 {
		return []string{prefix + message}
	}
	var out []string
	remaining := message
	for len(prefix)+len(remaining) > width {
		cut := bodyWidth
		if idx := strings.LastIndexByte(remaining[:bodyWidth], ' '); idx > 0 {
			cut = idx
		}
		out = append(out, prefix+strings.TrimRight(remaining[:cut], " "))
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	if remaining != "" {
		out = append(out, prefix+remaining)
	}
	return out
}

// AppendText adds a raw styled line to the log viewport.
func (l *LogView) AppendText(text string) {
	follow := !l.ready || l.viewport.AtBottom()
	l.lines.WriteString(text + "\n")
	if l.ready {
		l.viewport.SetContent(l.lines.String())
		if follow {
			l.viewport.GotoBottom()
		}
	}
}

// Transcript returns every line retained by the log pane. The service runner
// writes this back to the primary terminal after the alternate-screen TUI
// exits, so Ctrl-C leaves the run output in normal shell scrollback.
func (l *LogView) Transcript() string {
	return l.lines.String()
}

func (l *LogView) View() string {
	if !l.ready {
		return ""
	}
	return l.viewport.View()
}

func (l *LogView) SetSize(width, height int) {
	if !l.ready {
		l.viewport = viewport.New(width, height)
		l.viewport.SetContent(l.lines.String())
		l.ready = true
	} else {
		l.viewport.Width = width
		l.viewport.Height = height
	}
}
