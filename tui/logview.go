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
	s := Styles()
	var style func(strs ...string) string
	switch msg.Level {
	case wool.INFO:
		style = s.LogInfo.Render
	case wool.WARN:
		style = s.LogWarn.Render
	case wool.ERROR:
		style = s.LogError.Render
	case wool.DEBUG:
		style = s.LogDebug.Render
	case wool.FORWARD:
		style = s.LogForward.Render
	case wool.TRACE:
		style = s.LogTrace.Render
	default:
		style = s.LogForward.Render
	}
	line := style(fmt.Sprintf("%s %s", msg.Source, msg.Message))
	l.lines.WriteString(line + "\n")
	if l.ready {
		l.viewport.SetContent(l.lines.String())
		l.viewport.GotoBottom()
	}
}

// AppendText adds a raw styled line to the log viewport.
func (l *LogView) AppendText(text string) {
	l.lines.WriteString(text + "\n")
	if l.ready {
		l.viewport.SetContent(l.lines.String())
		l.viewport.GotoBottom()
	}
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
