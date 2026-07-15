package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/wool"
	"golang.org/x/term"
)

// MilestoneMarker prefixes aggregate lifecycle milestones (a named service
// loading, starting, becoming ready). It is the ">>" half of the shared log
// taxonomy — ">" is codefly narrating about one service and "|" is the
// service's own output, both emitted by the CLI's streaming logger. Keeping
// every milestone on this one marker, in one tense, lets headless and
// interactive runs read identically.
const MilestoneMarker = ">>"

// ServiceRunnerModel is a Bubbletea model for running a Codefly service.
type ServiceRunnerModel struct {
	service string
	// dependencies are the other services this run manages (e.g. neo4j,
	// postgres). On quit they are torn down together with the origin — none
	// stay alive — so the shutdown view names them explicitly.
	dependencies []string
	logView      LogView
	statusBar    StatusBar
	spinner      spinner.Model
	state        ServiceState
	quitting     bool
	width        int
	height       int
	// plan tracks every managed service so the header's "now / next" line and
	// the footer tally can show what is in flight and what is still pending —
	// the per-service visibility the single origin state never had.
	plan planView
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
		plan:      newPlanView(),
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
		// Every other key drives the log pane's scrollback (PgUp/PgDn, arrows,
		// Home/End, u/d, b/f, g/G — the viewport's default keymap) so the user
		// can review earlier output. Without forwarding these the pane was
		// frozen at the tail.
		cmds = append(cmds, m.logView.Update(msg))
		m.statusBar.SetFollowing(m.logView.AtBottom())

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve 2 header lines (title + now/next) and 1 status bar. The
		// now/next line is always allotted — kept blank when idle — so the log
		// pane never jumps height as services come and go.
		vpHeight := m.height - 4
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.logView.SetSize(m.width, vpHeight)
		m.statusBar.SetWidth(m.width)

	case ServicePlanMsg:
		m.plan.setPlan(msg.Services)

	case ServiceStateMsg:
		m.plan.setState(msg.Service, msg.State, time.Now())
		// The origin drives the header spinner and transcript; dependency
		// transitions only update the per-service plan, not the origin state.
		if msg.Service == m.service {
			m.state = msg.State
			m.statusBar.SetState(msg.State)
		}
		m.logView.AppendText(Styles().LogInfo.Render(
			fmt.Sprintf("%s %s: %s", MilestoneMarker, msg.Service, msg.State)))

	case ServiceFailedMsg:
		m.plan.setFailed(msg.Service, time.Now())
		m.logView.AppendText(Styles().LogError.Render(
			fmt.Sprintf("%s %s: %s", MilestoneMarker, msg.Service, StateFailed)))

	case ServiceReadyMsg:
		m.plan.setReady(msg.Service, msg.Port, time.Now())
		if msg.Service == m.service {
			m.state = StateRunning
			m.statusBar.SetState(StateRunning)
		}
		line := fmt.Sprintf("%s %s: %s", MilestoneMarker, msg.Service, StateRunning)
		if msg.Port > 0 {
			line += fmt.Sprintf(" on :%d", msg.Port)
		}
		m.logView.AppendText(Styles().Service.Render(line))

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

	case StopPlanMsg:
		m.dependencies = msg.Dependencies

	case ServiceLogMsg:
		cmd := m.logView.Update(msg)
		cmds = append(cmds, cmd)
		m.statusBar.SetFollowing(m.logView.AtBottom())

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
		// Name exactly what is going down. flow.Stop() tears down the origin AND
		// its dependencies together — nothing stays running — so spell that out
		// instead of a vague "Stopping service...". (nix-run service DATA on disk
		// survives; the processes do not.)
		msg := "Stopping " + m.service
		if len(m.dependencies) > 0 {
			msg += " + dependencies (" + strings.Join(m.dependencies, ", ") + ")"
		}
		msg += "..."
		return Styles().Muted.Render(msg) + "\n"
	}

	s := Styles()
	now := time.Now()

	header := s.Header.Render("codefly") + " " + s.Service.Render(m.service)
	if m.state != StateRunning && m.state != StateStopped {
		header += " " + m.spinner.View()
	}
	header += "  " + s.Muted.Render(fmtElapsed(now.Sub(m.statusBar.startedAt)))
	header += "\n"

	// Second header line: live "now / next" across all services. Always
	// present (blank when idle) so the log pane height stays fixed.
	header += m.plan.nowNextLine(now) + "\n"

	m.statusBar.SetTally(m.plan.tally(now))
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

// SendPlan seeds the set of managed services (dependency order, origin last)
// so the live status can show "what's next" before anything has started.
func (t *ServiceTUI) SendPlan(services []string) {
	t.p.Send(ServicePlanMsg{Services: services})
}

// SendState reports a lifecycle state change.
func (t *ServiceTUI) SendState(service string, state ServiceState) {
	t.p.Send(ServiceStateMsg{Service: service, State: state})
}

// SendFailed flips one service to Failed in the live status without ending the
// whole run.
func (t *ServiceTUI) SendFailed(service string) {
	t.p.Send(ServiceFailedMsg{Service: service})
}

// SendReady marks the service as running.
func (t *ServiceTUI) SendReady(service string, port int) {
	t.p.Send(ServiceReadyMsg{Service: service, Port: port})
}

// SendError reports a fatal error.
func (t *ServiceTUI) SendError(err error) {
	t.p.Send(ServiceErrorMsg{Err: err})
}

// SendStopPlan tells the runner which dependency services it manages, so the
// shutdown view can name what gets torn down on quit.
func (t *ServiceTUI) SendStopPlan(dependencies []string) {
	t.p.Send(StopPlanMsg{Dependencies: dependencies})
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

func protectTUIWorker(name string, report func(error), worker func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			report(fmt.Errorf("%s panicked: %v", name, recovered))
		}
	}()
	worker()
}

// RunServiceTUI creates and runs a full-screen TUI for a service.
// startFn is called in a goroutine to start the service flow; it should
// call tui.SendReady/SendError when the flow reaches steady state.
// The TUI blocks until Ctrl+C or SendDone. Returns after the TUI exits.
//
// It runs on the alternate screen buffer so the run owns the whole terminal
// and the log pane is independently scrollable (PgUp/PgDn/arrows) while the
// shell's own scrollback stays clean during the run. On exit, the retained
// transcript is printed back to the primary screen so the just-finished run is
// still available in normal terminal scrollback.
func RunServiceTUI(service string, logCh <-chan agents.ChannelLog, startFn func(t *ServiceTUI)) error {
	// Capture the terminal's cooked state before Bubbletea switches it to raw
	// mode, and force-restore it after the program exits. Raw mode clears the
	// OPOST/ONLCR output flag, so a bare "\n" no longer maps to CR+LF. If the
	// restore is skipped or raced on the failure path (it is), every post-TUI
	// line — the error chain, the shutdown logs — staircases to the right.
	// Restoring the saved termios re-enables LF→CRLF, so they print cleanly.
	// stdin and stdout share the tty device, so restoring via stdin's fd fixes
	// stdout's newline translation too.
	fd := int(os.Stdin.Fd())
	var saved *term.State
	if term.IsTerminal(fd) {
		saved, _ = term.GetState(fd)
	}

	m := newServiceRunnerModel(service)
	// Alternate screen → the run owns the terminal and the log pane scrolls on
	// its own without disturbing the shell's scrollback. Mouse is intentionally
	// NOT captured so the user can still select and copy log text with the
	// mouse; scrollback is driven from the keyboard (PgUp/PgDn/arrows/Home/End).
	p := tea.NewProgram(m, tea.WithAltScreen())

	t := &ServiceTUI{p: p}
	workerPanics := make(chan error, 2)
	reportWorkerPanic := func(err error) {
		select {
		case workerPanics <- err:
		default:
		}
		t.SendDone(err)
	}

	go protectTUIWorker("TUI log pump", reportWorkerPanic, func() {
		t.PumpLogs(logCh)
	})
	go protectTUIWorker("TUI service worker", reportWorkerPanic, func() {
		startFn(t)
	})

	// Restore the terminal while unwinding as well as on a normal return. The
	// nested scope keeps restoration before transcript output on the success
	// path, while its defer still runs if Bubbletea itself panics.
	finalModel, err := func() (tea.Model, error) {
		if saved != nil {
			defer func() { _ = term.Restore(fd, saved) }()
		}
		return p.Run()
	}()

	select {
	case panicErr := <-workerPanics:
		err = errors.Join(err, panicErr)
	default:
	}
	printTranscript(finalModel)
	return err
}

func printTranscript(model tea.Model) {
	transcript := transcriptFromModel(model)
	if transcript == "" {
		return
	}
	fmt.Fprint(os.Stdout, transcript)
	if !strings.HasSuffix(transcript, "\n") {
		fmt.Fprintln(os.Stdout)
	}
}

func transcriptFromModel(model tea.Model) string {
	switch m := model.(type) {
	case ServiceRunnerModel:
		return m.logView.Transcript()
	case *ServiceRunnerModel:
		if m == nil {
			return ""
		}
		return m.logView.Transcript()
	default:
		return ""
	}
}
