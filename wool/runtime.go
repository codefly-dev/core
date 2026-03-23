package wool

import "sync"

// IsDebug returns true if the global log level is DEBUG or TRACE.
func IsDebug() bool {
	return globalLogLevel == DEBUG || globalLogLevel == TRACE
}

var lock *sync.Mutex

func init() {
	lock = &sync.Mutex{}
}

// GlobalLogLevel returns the current global log level (thread-safe).
func GlobalLogLevel() Loglevel {
	lock.Lock()
	defer lock.Unlock()
	return globalLogLevel
}

// SetGlobalLogLevel sets the global log level (thread-safe).
func SetGlobalLogLevel(loglevel Loglevel) {
	lock.Lock()
	defer lock.Unlock()
	globalLogLevel = loglevel
}

var globalLogLevel = INFO

// fallbackLogger is used by Get() when no Provider is found in context.
// Set this to redirect orphan logs (e.g. from context.Background()) away
// from Console/stdout, which is critical when a TUI owns the terminal.
var fallbackLogger LogProcessor

// SetFallbackLogger sets (or clears) the global fallback LogProcessor.
// Pass nil to revert to the default Console logger.
func SetFallbackLogger(l LogProcessor) {
	lock.Lock()
	defer lock.Unlock()
	fallbackLogger = l
}

// getFallbackLogger returns the fallback logger or a Console if none is set.
func getFallbackLogger() LogProcessor {
	lock.Lock()
	defer lock.Unlock()
	if fallbackLogger != nil {
		return fallbackLogger
	}
	return &Console{level: globalLogLevel}
}
