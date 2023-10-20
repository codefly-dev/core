package shared

import (
	"fmt"
	"github.com/codefly-dev/golor"
	"github.com/fatih/color"
	"github.com/pkg/errors"
	"os"
	runtimedebug "runtime/debug"
	"strings"
)

// TODO: logger level
var debug bool
var trace bool

var todo bool

var todos map[string]bool

func init() {
	todos = make(map[string]bool)
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

func Debug() bool {
	return debug
}

func Todo() bool {
	return todo
}

func Trace() bool {
	return trace
}

type Logger struct {
	action string
	debug  bool
	trace  bool
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

// BaseLogger is the Minimum logger interface
type BaseLogger interface {
	SetDebug()
	SetTrace()
	Tracef(format string, args ...any)
	Debugf(format string, args ...any)
	DebugMe(format string, args ...any)
	TODO(format string, args ...any)
	Wrapf(err error, format string, args ...any) error
	Errorf(format string, args ...any) error
}

func (l *Logger) SetDebug() {
	l.debug = true
}

func (l *Logger) SetTrace() {
	l.trace = true
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

func (l *Logger) Tracef(format string, args ...any) {
	if Trace() || l.trace {
		c := color.New(color.FgGreen, color.Italic)
		c.Printf("[TRACE] (%s) ", l.action)
		c.Printf(format, args...)
		c.Println()
	}

}

func (l *Logger) Debugf(format string, args ...any) {
	if Debug() || Trace() || l.debug || l.trace {
		c := color.New(color.FgHiGreen, color.Italic)
		c.Printf("[DEBUG] (%s) ", l.action)
		c.Printf(format, args...)
		c.Println()
	}
}

func (l *Logger) DebugMe(format string, args ...any) {
	if Debug() || Trace() {
		c := color.New(color.Bold, color.FgRed)
		c.Printf("[DEBUG] (%s) ", l.action)
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

/*
Useful errors
*/

// UserError: something went quite wrong from the user "side"

type UserError struct {
	Value      string
	Suggestion string
}

func (u *UserError) WithSuggestion(s string) *UserError {
	u.Suggestion = s
	return u
}

func (u *UserError) Error() string {
	return golor.Sprintf(`{{.Value}}
{{.Suggestion}}`, u)
}

func NewUserError(format string, args ...any) *UserError {
	return &UserError{Value: fmt.Sprintf(format, args...)}
}

func IsUserError(err error) bool {
	var userError *UserError
	ok := errors.As(err, &userError)
	return ok
}

func UserErrorMessage(err error) string {
	var userError *UserError
	ok := errors.As(err, &userError)
	if !ok {
		Exit("should have a user error: got %T", err)
	}
	return strings.TrimSpace(userError.Error())
}

// UserWarning: something went somewhat wrong from the user "side"

type UserWarning struct {
	value string
}

func (u *UserWarning) Error() string {
	return u.value
}

func NewUserWarning(format string, args ...any) error {
	return &UserWarning{value: fmt.Sprintf(format, args...)}
}

func IsUserWarning(err error) bool {
	var userWarning *UserWarning
	ok := errors.As(err, &userWarning)
	return ok
}

func UserWarnMessage(err error) string {
	var userWarning *UserWarning
	ok := errors.As(err, &userWarning)
	if !ok {
		Exit("should have a user warning: got %T", err)
	}
	return userWarning.Error()
}

// OutputError: encapsulates the output of a command

type OutputError struct {
	value string
}

func (u *OutputError) Error() string {
	return u.value
}

func NewOutputError(format string, args ...any) error {
	return &OutputError{value: fmt.Sprintf(format, args...)}
}

func IsOutputError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	var outputError *OutputError
	ok := errors.As(err, &outputError)
	return outputError, ok
}
