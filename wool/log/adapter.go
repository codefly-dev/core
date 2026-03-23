package log

import (
	"fmt"

	"github.com/codefly-dev/core/wool"
)

// Logger is a simple adapter that wraps Wool into a Printf-style interface.
type Logger struct {
	w *wool.Wool
}

// AsLog creates a Logger from a Wool instance.
func AsLog(w *wool.Wool) *Logger {
	return &Logger{w: w}
}

// Info logs a formatted message at INFO level.
func (l *Logger) Info(message string, args ...any) {
	l.w.Info(fmt.Sprintf(message, args...))
}
