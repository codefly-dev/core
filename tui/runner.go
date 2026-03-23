package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/wool"
)

// ServiceRunnerModel is a Bubbletea model for running a Codefly service.
type ServiceRunnerModel struct {
	service   string
	logView   LogView
	statusBar StatusBar
	spinner   spinner.Model
	state     ServiceState
	quitting  bool
	width     int
	height    int
}

func newServiceRunnerModel(service string) ServiceRunnerModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = Styles().Spinner

	return ServiceRunnerModel{
		service:   service,
		logView:   NewLogView(),
		statusBar: NewStatusBar(service),
		spinner:   sp,
		state:     StateLoading,
	}
}

func (m ServiceRunnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickEvery())
}

func (m ServiceRunnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			m.state = StateStopping
			m.statusBar.SetState(StateStopping)
			m.logView.AppendText(Styles().Muted.Render("Shutting down..."))
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpHeight := m.height - 3
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.logView.SetSize(m.width, vpHeight)
		m.statusBar.SetWidth(m.width)

	case ServiceStateMsg:
		m.state = msg.State
		m.statusBar.SetState(msg.State)
		m.logView.AppendText(Styles().LogInfo.Render(
			fmt.Sprintf(">> %s: %s", msg.Service, msg.State)))

	case ServiceReadyMsg:
		m.state = StateRunning
		m.statusBar.SetState(StateRunning)
		if msg.Port > 0 {
			m.logView.AppendText(Styles().Service.Render(
				fmt.Sprintf(">> %s running on :%d", msg.Service, msg.Port)))
		} else {
			m.logView.AppendText(Styles().Service.Render(
				fmt.Sprintf(">> %s is running", msg.Service)))
		}

	case ServiceErrorMsg:
		m.state = StateFailed
		m.statusBar.SetState(StateFailed)
		m.logView.AppendText(Styles().LogError.Render(
			fmt.Sprintf("ERROR: %v", msg.Err)))

	case FlowDoneMsg:
		if msg.Err != nil {
			m.state = StateFailed
			m.statusBar.SetState(StateFailed)
			m.logView.AppendText(Styles().LogError.Render(
				fmt.Sprintf("Flow error: %v", msg.Err)))
		}
		return m, tea.Quit

	case ServiceLogMsg:
		cmd := m.logView.Update(msg)
		cmds = append(cmds, cmd)

	case tickMsg:
		cmd := m.statusBar.Update(msg)
		cmds = append(cmds, cmd)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m ServiceRunnerModel) View() string {
	if m.quitting {
		return Styles().Muted.Render("Stopping service...") + "\n"
	}

	s := Styles()
	header := s.Header.Render("codefly") + " " + s.Service.Render(m.service)
	if m.state != StateRunning && m.state != StateStopped {
		header += " " + m.spinner.View()
	}
	header += "\n"

	return header + m.logView.View() + "\n" + m.statusBar.View()
}

// ServiceTUI controls a running service TUI. Callers use Send* methods
// to push events without knowing about Bubbletea.
type ServiceTUI struct {
	p *tea.Program
}

// SendLog pushes a log entry into the TUI viewport.
func (t *ServiceTUI) SendLog(level wool.Loglevel, source, message string) {
	t.p.Send(ServiceLogMsg{Level: level, Source: source, Message: message})
}

// SendState reports a lifecycle state change.
func (t *ServiceTUI) SendState(service string, state ServiceState) {
	t.p.Send(ServiceStateMsg{Service: service, State: state})
}

// SendReady marks the service as running.
func (t *ServiceTUI) SendReady(service string, port int) {
	t.p.Send(ServiceReadyMsg{Service: service, Port: port})
}

// SendError reports a fatal error.
func (t *ServiceTUI) SendError(err error) {
	t.p.Send(ServiceErrorMsg{Err: err})
}

// SendDone signals the flow has completed.
func (t *ServiceTUI) SendDone(err error) {
	t.p.Send(FlowDoneMsg{Err: err})
}

// NewLogChannel creates a buffered channel and registers a processor so
// agent logs flow into the channel. Call this before starting agents.
func NewLogChannel() <-chan agents.ChannelLog {
	ch := make(chan agents.ChannelLog, 256)
	agents.AddProcessor(agents.NewChannelProcessor(ch))
	return ch
}

// PumpLogs reads from logCh and forwards entries to the TUI.
// Blocks until the channel is closed; run in a goroutine.
func (t *ServiceTUI) PumpLogs(logCh <-chan agents.ChannelLog) {
	for log := range logCh {
		source := ""
		if log.Source != nil {
			source = log.Source.Unique
		}
		level := wool.INFO
		if log.Log != nil {
			level = log.Log.Level
		}
		msg := ""
		if log.Log != nil {
			msg = log.Log.Message
		}
		t.SendLog(level, source, msg)
	}
}

// RunServiceTUI creates and runs an inline TUI for a service.
// startFn is called in a goroutine to start the service flow; it should
// call tui.SendReady/SendError when the flow reaches steady state.
// The TUI blocks until Ctrl+C or SendDone. Returns after the TUI exits.
// Output remains visible in the terminal after exit for debugging.
func RunServiceTUI(service string, logCh <-chan agents.ChannelLog, startFn func(t *ServiceTUI)) error {
	m := newServiceRunnerModel(service)
	p := tea.NewProgram(m)

	t := &ServiceTUI{p: p}

	go t.PumpLogs(logCh)
	go startFn(t)

	_, err := p.Run()
	return err
}
