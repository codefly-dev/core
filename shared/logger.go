package shared

import (
	"context"
	"fmt"
	"os"
	runtimedebug "runtime/debug"
	"strings"

	"github.com/fatih/color"
	"github.com/pkg/errors"
)

type LogLevel int

const (
	Trace LogLevel = iota
	Debug
	Info
	Warn
)

type ContextLoggerKey string

const (
	Base    = ContextLoggerKey("base")
	Agent   = ContextLoggerKey("agent")
	Service = ContextLoggerKey("service")
)

// GetLogger returns the logger from the context
// - Base
// - Agent
func GetLogger(ctx context.Context) BaseLogger {
	var logger BaseLogger
	l := ctx.Value(Base)
	if l != nil {
		logger = l.(BaseLogger)
	} else {
		l = ctx.Value(Agent)
		if l == nil {
			panic("no logger in context")
		}
	}
	return logger
	//child := NewLogger()
	//child.lvl = logger.LogLevel()
	//child.logMode = logger.LogMode()
	//for _, action := range logger.actions {
	//	child.actions = append(child.actions, action)
	//}
	//return child
}

func GetAgentLogger(ctx context.Context) BaseLogger {
	return ctx.Value(Agent).(BaseLogger)
}

func GetServiceLogger(ctx context.Context) BaseLogger {
	return ctx.Value(Service).(BaseLogger)
}

// BaseLogger is the Minimum logger interface
type BaseLogger interface {
	SetLevel(lvl LogLevel) BaseLogger
	SetLogMethod(actions LogMode) BaseLogger
	With(format string, args ...any) BaseLogger

	Info(format string, args ...any)

	Tracef(format string, args ...any)
	Debugf(format string, args ...any)
	DebugMe(format string, args ...any)
	Warn(format string, args ...any)

	TODO(format string, args ...any)
	// Wrap uses action to wrap the error
	Wrap(err error) error

	// Wrapf uses action to wrap the error and other message
	Wrapf(err error, format string, args ...any) error

	Errorf(format string, args ...any) error
	Oops(format string, args ...any)

	// Write does the actual work
	Write(p []byte) (n int, err error)
	WarnOnError(err error)
}

var (
	level = Info
)

var (
	todo     bool
	override bool
)

var (
	todos map[string]bool
	warns map[string]bool
)

func init() {
	todos = make(map[string]bool)
	warns = make(map[string]bool)
}

func SetLogLevel(lvl LogLevel) {
	level = lvl
}

func SetTodo(t bool) {
	todo = t
}

func SetOverride(o bool) {
	override = o
}

func IsDebug() bool {
	return level == Debug || level == Trace
}

func Todo() bool {
	return todo || IsTrace() || IsDebug()
}

func IsTrace() bool {
	return level == Trace
}

func GlobalOverride() bool {
	return override
}

type Logger struct {
	actions []string
	lvl     LogLevel
	logMode LogMode
}

func (l *Logger) Warn(format string, args ...any) {
	l.TODO(format, args...)
}

func NewLogger() *Logger {
	return &Logger{
		lvl:     Info,
		logMode: StartAndLastAction}
}

func (l *Logger) SetLevel(lvl LogLevel) BaseLogger {
	l.lvl = lvl
	return l
}

func (l *Logger) SetLogMethod(mode LogMode) BaseLogger {
	l.logMode = mode
	return l
}

func (l *Logger) With(format string, args ...any) BaseLogger {
	action := fmt.Sprintf(format, args...)
	l.actions = append(l.actions, action)
	return l
}

type LogMode string

const (
	LastAction         = LogMode("last_action")
	StartAndLastAction = LogMode("start_and_last_action")
	AllActions         = LogMode("all_actions")
)

func (l *Logger) SetLogMode(mode LogMode) {
	l.logMode = mode
}

func (l *Logger) Action() string {
	if len(l.actions) == 0 {
		return ""
	}
	sep := " -> "
	var ss []string
	switch l.logMode {
	case LastAction:
		ss = append(ss, l.actions[len(l.actions)-1])
	case StartAndLastAction:
		sep = " ---> "
		if len(l.actions) == 1 {
			ss = append(ss, l.actions[0])
		} else {
			ss = append(ss, l.actions[0], l.actions[len(l.actions)-1])
		}
	case AllActions:
		ss = append(ss, l.actions...)
	}

	return fmt.Sprintf("(%s)", strings.Join(ss, sep))
}

func (l *Logger) Write(p []byte) (n int, err error) {
	return fmt.Print(string(p))
}

func (l *Logger) Errorf(format string, args ...any) error {
	return errors.Wrap(errors.Errorf(format, args...), l.Action())
}

func (l *Logger) Wrap(err error) error {
	return errors.Wrapf(err, l.Action())
}

func (l *Logger) Wrapf(err error, format string, args ...any) error {
	if len(l.actions) > 0 {
		format = fmt.Sprintf("%s: %s", l.Action(), format)
	}
	return errors.Wrapf(err, format, args...)
}

func Wrapf(err error, format string, args ...any) error {
	return errors.Wrapf(err, format, args...)
}

func (l *Logger) Info(format string, args ...any) {
	fmt.Printf(format, args...)
	fmt.Println()
}

func (l *Logger) Tracef(format string, args ...any) {
	if IsTrace() || l.lvl == Trace {
		c := color.New(color.FgGreen)
		c.Printf("[TRACE]%s ", l.Action())
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) Debugf(format string, args ...any) {
	if IsDebug() || IsTrace() || l.lvl <= Debug {
		c := color.New(color.FgHiGreen, color.Bold)
		c.Printf("[DEBUG]%s ", l.Action())
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) DebugMe(format string, args ...any) {
	if IsDebug() || IsTrace() || l.lvl <= Debug {
		c := color.New(color.Bold, color.FgHiWhite, color.BgRed)
		c.Printf("[DEBUG ME]%s ", l.Action())
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) Oops(format string, args ...any) {
	c := color.New(color.FgHiWhite, color.Bold)
	c.Print("🤭")
	c.Printf(format, args...)
	c.Println()
}

func (l *Logger) TODO(format string, args ...any) {
	todo := fmt.Sprintf(fmt.Sprintf("⚠️TODO [%s] => %s", l.actions[len(l.actions)-1], format), args...)
	if Todo() {
		if !todos[format] {
			todos[format] = true
			c := color.New(color.FgHiWhite, color.Bold)
			c.Printf(todo)
			c.Println()
		}
	}
}

func (l *Logger) WarnOnError(err error) {
	c := color.New(color.FgHiWhite)
	c.Print("⚠️ ")
	c.Printf(UserWarnMessage(err))
	c.Println()
}

func (l *Logger) WarnUnique(err error) {
	if !warns[err.Error()] {
		l.WarnOnError(err)
		warns[err.Error()] = true

	}
}

func (l *Logger) UserFatal(err error) {
	msg := l.UserFatalMessage(err)
	l.Oops(msg)
	os.Exit(1)
}

func (l *Logger) UserFatalMessage(err error) string {
	var userError *UserError
	ok := errors.As(err, &userError)
	if !ok {
		Exit("should have a user error: got %T", err)
	}
	return userError.Error()
}

func (l *Logger) Message(format string, args ...any) {
	fmt.Printf(format, args...)
	fmt.Println()
}

func (l *Logger) Catch() {
	if r := recover(); r != nil {
		fmt.Println("Exiting the CLI unexpectedly")
		if IsDebug() {
			fmt.Println(r)
			fmt.Println(string(runtimedebug.Stack()))
		}
	}
}

func (l *Logger) Actions() []string {
	return l.actions
}
