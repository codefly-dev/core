package log

import (
	"fmt"

	"github.com/codefly-dev/core/wool"
)

type Logger struct {
	context *wool.Context
}

func AsLog(context *wool.Context) *Logger {
	return &Logger{
		context: context,
	}
}

func (l *Logger) Info(message string, args ...any) {
	l.context.Info(fmt.Sprintf(message, args...))
}
