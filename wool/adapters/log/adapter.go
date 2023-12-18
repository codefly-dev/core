package log

import (
	"fmt"

	"github.com/codefly-dev/core/wool"
)

type Logger struct {
	w *wool.Wool
}

func AsLog(w *wool.Wool) *Logger {
	return &Logger{
		w: w,
	}
}

func (l *Logger) Info(message string, args ...any) {
	l.w.Info(fmt.Sprintf(message, args...))
}
