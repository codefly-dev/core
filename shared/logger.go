package shared

import (
	"fmt"
	"os"
	runtimedebug "runtime/debug"

	"github.com/fatih/color"
	"github.com/pkg/errors"
)

type LogLevel = string

type ContextLoggerKey string

const Plugin = ContextLoggerKey("plugin")
const Service = ContextLoggerKey("service")

const (
	TraceLevel LogLevel = "trace"
	DebugLevel LogLevel = "debug"
)

// BaseLogger is the Minimum logger interface
type BaseLogger interface {
	Write(p []byte) (n int, err error)

	SetLevel(lvl LogLevel)

	Info(format string, args ...any)

	Tracef(format string, args ...any)
	Debugf(format string, args ...any)
	DebugMe(format string, args ...any)
	TODO(format string, args ...any)

	Wrapf(err error, format string, args ...any) error
	Errorf(format string, args ...any) error
}

// TODO: logger level
var (
	debug bool
	trace bool
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

func SetDebug(d bool) {
	debug = d
}

func SetTodo(t bool) {
	todo = t
}

func SetTrace(t bool) {
	trace = t
}

func SetOverride(o bool) {
	override = o
}

func Debug() bool {
	return debug
}

func Todo() bool {
	return todo
}

func Trace() bool {
	return trace
}

func Override() bool {
	return override
}

type Logger struct {
	action string
	debug  bool
	trace  bool
}

func (l *Logger) SetLevel(lvl LogLevel) {
	if lvl == TraceLevel {
		l.trace = true
		l.debug = true
	} else if lvl == DebugLevel {
		l.debug = true
	}
}

func NewLogger(action string, args ...any) *Logger {
	return &Logger{action: fmt.Sprintf(action, args...)}
}

func (l *Logger) IfNot(base BaseLogger) BaseLogger {
	if base != nil {
		return base
	}
	return l
}

func (l *Logger) Write(p []byte) (n int, err error) {
	return fmt.Print(string(p))
}

func (l *Logger) Errorf(format string, args ...any) error {
	return errors.Wrap(errors.Errorf(format, args...), l.action)
}

func (l *Logger) Wrapf(err error, format string, args ...any) error {
	format = fmt.Sprintf("%s: %s", l.action, format)
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
	if Trace() || l.trace {
		c := color.New(color.FgGreen)
		c.Printf("[TRACE] (%s) ", l.action)
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) Debugf(format string, args ...any) {
	if Debug() || Trace() || l.debug || l.trace {
		c := color.New(color.FgHiGreen, color.Bold)
		c.Printf("[DEBUG] (%s) ", l.action)
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) DebugMe(format string, args ...any) {
	if Debug() || Trace() || l.debug || l.trace {
		c := color.New(color.Bold, color.FgHiWhite, color.BgRed)
		c.Printf("[DEBUG ME] (%s) ", l.action)
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
	todo := fmt.Sprintf(fmt.Sprintf("⚠️TODO [%s] => %s", l.action, format), args...)
	if Todo() {
		if !todos[format] {
			todos[format] = true
			c := color.New(color.FgHiWhite, color.Bold)
			c.Printf(todo)
			c.Println()
		}
	}
}

func (l *Logger) Warn(err error) {
	c := color.New(color.FgHiWhite)
	c.Print("⚠️ ")
	c.Printf(UserWarnMessage(err))
	c.Println()
}

func (l *Logger) WarnUnique(err error) {
	if !warns[err.Error()] {
		l.Warn(err)
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
		if Debug() {
			fmt.Println(r)
			fmt.Println(string(runtimedebug.Stack()))
		}
	}
}
