package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"runtime/debug"
	"time"

	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/charmbracelet/lipgloss"
	"github.com/codefly-dev/core/configurations"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	"github.com/codefly-dev/core/shared"
	"github.com/hashicorp/go-hclog"
	"github.com/pkg/errors"
)

/*
logger used to take the output of the service
*/

type ServiceLogger struct {
	transport       hclog.Logger
	AgentIdentifier string
	Service         string
	Application     string
	JSON            bool
	action          string
	trace           bool
	debug           bool
}

func (l *ServiceLogger) Warn(format string, args ...any) {
	//TODO implement me
	panic("implement me")
}

func (l *ServiceLogger) SetLogMethod(actions shared.LogMode) shared.BaseLogger {
	//TODO implement me
	panic("implement me")
}

func (l *ServiceLogger) WarnOnError(err error) {
	//TODO implement me
	panic("implement me")
}

func (l *ServiceLogger) Oops(format string, args ...any) {
	//TODO implement me
	panic("implement me")
}

func (l *ServiceLogger) SetLevel(shared.LogLevel) shared.BaseLogger {
	return l
}

func NewServiceLogger(identity *servicev1.ServiceIdentity, agent *configurations.Agent) *ServiceLogger {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	return &ServiceLogger{
		transport:       logger,
		AgentIdentifier: agent.Name,
		Application:     identity.Application,
		Service:         identity.Name,
	}
}

func (l *ServiceLogger) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	entry := LogEntry{
		Msg:             string(p),
		Kind:            ServiceKind,
		AgentIdentifier: l.AgentIdentifier,
		Service:         l.Service,
		Application:     l.Application,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		// Log the error to a fallback logger or stderr.
		return fmt.Fprintf(os.Stderr, "Could not marshal log entry: %v\n", err)
	}

	writer := l.transport.StandardWriter(&hclog.StandardLoggerOptions{})
	n, err = writer.Write(data)
	if err != nil {
		// Log the error to a fallback logger or stderr.
		return fmt.Fprintf(os.Stderr, "Could not write to StandardWriter: %v\n", err)
	}
	return n, err
}

func (l *ServiceLogger) UnsafeWrite(s string) {
	_, err := l.Write([]byte(s))
	if err != nil {
		panic(err)
	}
}

func (l *ServiceLogger) Info(format string, args ...any) {
	l.UnsafeWrite(fmt.Sprintf(format, args...))
}

func (l *ServiceLogger) Tracef(format string, args ...any) {
	if l.trace {
		l.UnsafeWrite(fmt.Sprintf(format, args...))
	}
}

func (l *ServiceLogger) Debugf(format string, args ...any) {
	if l.trace || l.debug {
		l.UnsafeWrite(fmt.Sprintf(format, args...))
	}
}

func (l *ServiceLogger) DebugMe(format string, args ...any) {
	l.UnsafeWrite(fmt.Sprintf(format, args...))
}

func (l *ServiceLogger) TODO(format string, args ...any) {
	if _, ok := todos[format]; ok {
		return
	}
	todos[format] = true
	l.UnsafeWrite("⚠️TODO " + fmt.Sprintf(format, args...))
}

func (l *ServiceLogger) With(format string, args ...any) shared.BaseLogger {
	l.action = fmt.Sprintf(format, args...)
	return l
}

func (l *ServiceLogger) Wrap(err error) error {
	if l.action == "" {
		return err
	}
	return errors.Wrapf(err, l.action)
}

func (l *ServiceLogger) Wrapf(err error, format string, args ...any) error {
	if l.action != "" {
		format = fmt.Sprintf("%s: %s", l.action, format)
	}
	return errors.Wrapf(err, format, args...)
}

func (l *ServiceLogger) Errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

/*
logger used by agent surrounding
*/

// TODO: Hook std.Out to it

// NewLogger returns a Logger returns JSON with the following fields:
// @message
// @specialization

type AgentLogger struct {
	transport       hclog.Logger
	AgentIdentifier string
	Service         string
	Application     string
	debug           bool
	trace           bool
	action          string
}

func (l *AgentLogger) Warn(format string, args ...any) {
	//TODO implement me
	panic("implement me")
}

func (l *AgentLogger) WarnOnError(err error) {
	//TODO implement me
	panic("implement me")
}

func (l *AgentLogger) SetLevel(lvl shared.LogLevel) shared.BaseLogger {
	//TODO implement me
	panic("implement me")
}

func (l *AgentLogger) SetLogMethod(actions shared.LogMode) shared.BaseLogger {
	//TODO implement me
	panic("implement me")
}

func (l *AgentLogger) Oops(format string, args ...any) {
	//TODO implement me
	panic("implement me")
}

func (l *AgentLogger) With(format string, args ...any) shared.BaseLogger {
	l.action = fmt.Sprintf(format, args...)
	return l
}

func (l *AgentLogger) SetDebug() {
	l.debug = true
	l.transport.SetLevel(hclog.Debug)
}

func (l *AgentLogger) SetTrace() {
	l.trace = true
	l.transport.SetLevel(hclog.Trace)
}

func (l *AgentLogger) Wrap(err error) error {
	if l.action == "" {
		return err
	}
	return errors.Wrapf(err, l.action)
}

func (l *AgentLogger) Wrapf(err error, format string, args ...any) error {
	if l.action != "" {
		format = fmt.Sprintf("%s: %s", l.action, format)
	}
	return errors.Wrapf(err, format, args...)
}

func NewAgentLogger(identity *servicev1.ServiceIdentity, agent *configurations.Agent) *AgentLogger {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	return &AgentLogger{
		AgentIdentifier: agent.Name,
		Application:     identity.Application,
		Service:         identity.Name,
		transport:       logger,
	}
}

type LogEntry struct {
	Msg             string
	AgentIdentifier string
	Service         string
	Application     string
	Kind            string
	DebugMe         bool
}

func (l *AgentLogger) WriteEntry(entry *LogEntry) (n int, err error) {
	data, err := json.Marshal(entry)
	if err != nil {
		// Log the error to a fallback logger or stderr.
		return fmt.Fprintf(os.Stderr, "Could not marshal log entry: %v\n", err)
	}

	writer := l.transport.StandardWriter(&hclog.StandardLoggerOptions{})
	n, err = writer.Write(data)
	if err != nil {
		// Log the error to a fallback logger or stderr.
		return fmt.Fprintf(os.Stderr, "Could not write to StandardWriter: %v\n", err)
	}
	return n, err
}

const (
	AgentKind   = "agent"
	ServiceKind = "service"
)

func (l *AgentLogger) NewLogEntry(b []byte) *LogEntry {
	return &LogEntry{
		Msg:             string(b),
		Kind:            AgentKind,
		AgentIdentifier: l.AgentIdentifier,
		Service:         l.Service,
		Application:     l.Application,
	}
}

func (l *AgentLogger) Write(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}
	return l.WriteEntry(l.NewLogEntry(b))
}

func (l *AgentLogger) UnsafeWrite(s string) {
	_, err := l.Write([]byte(s))
	if err != nil {
		panic(err)
	}
}

func (l *AgentLogger) Tracef(format string, args ...any) {
	if !l.trace {
		return
	}
	l.UnsafeWrite(fmt.Sprintf(format, args...))
}

func (l *AgentLogger) Debugf(format string, args ...any) {
	if !l.debug || l.trace {
		return
	}
	l.UnsafeWrite(fmt.Sprintf(format, args...))
}

func (l *AgentLogger) DebugMe(format string, args ...any) {
	if !l.debug {
		return
	}
	entry := l.NewLogEntry([]byte(fmt.Sprintf(format, args...)))
	entry.DebugMe = true
	_, _ = l.WriteEntry(entry)
}

var todos map[string]bool

func init() {
	todos = make(map[string]bool)
}

func (l *AgentLogger) TODO(format string, args ...any) {
	if !l.debug {
		return
	}
	if _, ok := todos[format]; ok {
		return
	}
	todos[format] = true

	entry := l.NewLogEntry([]byte(fmt.Sprintf(fmt.Sprintf("⚠️TODO %s", format), args...)))
	_, _ = l.WriteEntry(entry)
}

func (l *AgentLogger) Info(format string, args ...any) {
	l.UnsafeWrite(fmt.Sprintf(format, args...))
}

func (l *AgentLogger) Errorf(format string, args ...any) error {
	l.TODO("Implement with gRPC errors properly")
	return fmt.Errorf(format, args...)
}

func (l *AgentLogger) Catch() {
	if r := recover(); r != nil {
		l.Debugf("IN PANIC CATCH")
		l.Warn("PANIC CAUGHT INSIDE THE AGENT CODE -- STOPPING EVERYTHING: %v", r)
		l.Warn(string(debug.Stack()))
	}
}

/*
logger used by Codefly server
*/

var (
	logger hclog.Logger
	output *ServerFormatter
)

func init() {
	output = NewServerFormatter(shared.IsDebug())
}

func NewServerLogger() hclog.Logger {
	if logger != nil {
		return logger
	}

	logger = hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
		Output:     output,
		Level:      hclog.Debug,
	})
	return logger
}

type ColorPicker struct {
	foregroundColors []lipgloss.Color
	backgroundColors []lipgloss.Color
}

func generateForegroundColors() []lipgloss.Color {
	return []lipgloss.Color{
		lipgloss.Color("#ADD8E6"), // Light Blue
		lipgloss.Color("#90EE90"), // Soft Green
		lipgloss.Color("#FFC0CB"), // Pale Pink
		lipgloss.Color("#E6E6FA"), // Lavender
		lipgloss.Color("#F08080"), // Light Coral
		lipgloss.Color("#F5DEB3"), // Wheat
		lipgloss.Color("#00FF00"), // Bright Green
		lipgloss.Color("#00FFFF"), // Cyan
		lipgloss.Color("#FF1493"), // Neon Pink
		lipgloss.Color("#7DF9FF"), // Electric Blue
		lipgloss.Color("#FF69B4"), // Hot Pink
		lipgloss.Color("#C0C0C0"), // Silver
	}
}

func NewColorPicker() *ColorPicker {
	backgroundColors := []lipgloss.Color{
		lipgloss.Color("#333333"), lipgloss.Color("#444444"), // ... add more colors
	}
	return &ColorPicker{backgroundColors: backgroundColors, foregroundColors: generateForegroundColors()}
}

func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func (cp *ColorPicker) PickStyle(app string, service string) lipgloss.Style {
	hashApp := hashString(app)
	hashService := hashString(service)

	fgColor := cp.foregroundColors[hashApp%uint32(len(cp.foregroundColors))]
	bgColor := cp.backgroundColors[hashService%uint32(len(cp.backgroundColors))]

	return lipgloss.NewStyle().
		Foreground(fgColor).
		Background(bgColor)
}

type ServerFormatter struct {
	buffer    bytes.Buffer
	picker    *ColorPicker
	debug     bool
	callbacks []LogCallback
	styles    map[string]lipgloss.Style
}

func NewServerFormatter(debug bool) *ServerFormatter {
	return &ServerFormatter{
		picker: NewColorPicker(),
		styles: make(map[string]lipgloss.Style),
		debug:  debug,
	}
}

type LogCallback func(log *agentsv1.Log)

func RegisterLogCallback(callback LogCallback) {
	output.callbacks = append(output.callbacks, callback)
}

type LogMessage struct {
	Level     string    `json:"@level"`
	Timestamp time.Time `json:"@timestamp"`

	RawMessage string `json:"@message"`

	Message LogMessageContent
}

type LogMessageContent struct {
	Msg             string `json:"Msg"`
	Application     string `json:"Application"`
	Service         string `json:"Service"`
	Kind            string `json:"Kind"`
	AgentIdentifier string `json:"AgentIdentifier"`
	Level           string `json:"Level"`
}

func ToKind(s string) agentsv1.Log_Kind {
	switch s {
	case ServiceKind:
		return agentsv1.Log_SERVICE
	case AgentKind:
		return agentsv1.Log_AGENT
	default:
		return agentsv1.Log_UNKNOWN
	}
}

func createManagementLog(log *LogMessage) *agentsv1.Log {
	return &agentsv1.Log{
		At:          timestamppb.New(log.Timestamp),
		Kind:        ToKind(log.Message.Kind),
		Application: log.Message.Application,
		Service:     log.Message.Service,
		Message:     log.Message.Msg,
	}
}

func (out *ServerFormatter) Write(p []byte) (n int, err error) {
	n, err = out.buffer.Write(p)
	if err != nil {
		return
	}
	defer out.buffer.Reset()

	var log LogMessage
	err = json.Unmarshal(out.buffer.Bytes(), &log)
	if err != nil {
		fmt.Printf("got error unmarshalling log: %v\n", err)
		return
	}
	err = json.Unmarshal([]byte(log.RawMessage), &log.Message)
	if err != nil {
		log.Message = LogMessageContent{}
	}

	message := log.Message.Msg
	if message == "" {
		return
	}

	mgLog := createManagementLog(&log)
	// Send the management Log to registered callbacks
	for _, callback := range out.callbacks {
		callback(mgLog)
	}

	unique := fmt.Sprintf("%s/%s", log.Message.Application, log.Message.Service)

	var style lipgloss.Style
	var ok bool
	if style, ok = out.styles[unique]; !ok {
		out.styles[unique] = out.picker.PickStyle(log.Message.Application, log.Message.Service)
	}

	// debug me bool
	if log.Message.Level == "debug-me" {
		style = style.Copy().Background(lipgloss.Color("#FFD700")) // gold
	}
	sender := fmt.Sprintf("%s/%s", log.Message.Application, log.Message.Service)

	fmt.Println(style.Render(fmt.Sprintf("[%s] %s", sender, message)))
	return
}

func NoLogger() hclog.Logger {
	return hclog.NewNullLogger()
}
