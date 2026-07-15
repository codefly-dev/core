package wool

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// IsDebug returns true if the global log level is DEBUG or TRACE.
func IsDebug() bool {
	l := GlobalLogLevel()
	return l == DEBUG || l == TRACE
}

var globalLogLevel atomic.Int32

func init() { globalLogLevel.Store(int32(INFO)) }

// GlobalLogLevel returns the current global log level (thread-safe).
func GlobalLogLevel() Loglevel {
	return Loglevel(globalLogLevel.Load())
}

// SetGlobalLogLevel sets the global log level (thread-safe).
func SetGlobalLogLevel(loglevel Loglevel) {
	globalLogLevel.Store(int32(loglevel))
}

// fallbackLogger is used by Get() when no Provider is found in context.
// Set this to redirect orphan logs (e.g. from context.Background()) away
// from Console/stdout, which is critical when a TUI owns the terminal.
var fallbackLogger LogProcessor
var fallbackLoggerMu sync.RWMutex

// SetFallbackLogger sets (or clears) the global fallback LogProcessor.
// Pass nil to revert to the default Console logger.
func SetFallbackLogger(l LogProcessor) {
	fallbackLoggerMu.Lock()
	defer fallbackLoggerMu.Unlock()
	fallbackLogger = l
}

// getFallbackLogger returns the fallback logger or a Console if none is set.
func getFallbackLogger() LogProcessor {
	fallbackLoggerMu.RLock()
	defer fallbackLoggerMu.RUnlock()
	if fallbackLogger != nil {
		return fallbackLogger
	}
	return &Console{level: GlobalLogLevel()}
}

// scopeRule maps a scope-name prefix to a log level. A rule with an empty
// prefix is the catch-all (written "*" in the spec).
type scopeRule struct {
	prefix string
	level  Loglevel
}

// scopeRules holds the active per-scope overrides. It is read on the logging
// hot path (Wool.LogLevel, on essentially every log call) so reads go through a
// lock-free atomic load; the slice is replaced wholesale, never mutated in
// place. Seeded from CODEFLY_LOG at init and replaceable via SetLogScopes.
var scopeRules atomic.Pointer[[]scopeRule]

func init() {
	SetLogScopes(os.Getenv("CODEFLY_LOG"))
}

// SetLogScopes installs per-scope level overrides, replacing any previously set
// (including the CODEFLY_LOG defaults loaded at init). The spec is a comma list
// of "scope=level" entries, e.g. "network=debug,resources=info,*=warn"; "*" is
// the catch-all. A scope matches when its prefix lines up with a leading segment
// of the .In(...) name (see scopeMatches); the longest matching prefix wins.
// Unparseable entries are ignored.
func SetLogScopes(spec string) {
	rules := parseLogScopes(spec)
	scopeRules.Store(&rules)
}

func parseLogScopes(spec string) []scopeRule {
	var rules []scopeRule
	for part := range strings.SplitSeq(spec, ",") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		level, ok := LevelFromString(value)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue // malformed "=level"; only "*" is the catch-all
		}
		if name == "*" {
			name = ""
		}
		rules = append(rules, scopeRule{prefix: name, level: level})
	}
	return rules
}

// scopeMatches reports whether a non-empty rule prefix applies to a .In(...)
// scope name. It anchors on scope-segment boundaries ('.' or '::') so "network"
// matches "network.Runtime" and "RuntimeInstance::Load" matches "RuntimeInstance"
// — but "net" does not spuriously match "network".
func scopeMatches(name, prefix string) bool {
	if name == prefix {
		return true
	}
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	switch name[len(prefix)] {
	case '.', ':':
		return true
	default:
		return false
	}
}

// scopeLevelFor returns the level override for a .In(...) scope name. The
// longest matching prefix wins; the catch-all ("*") applies when no prefix
// matches. The second return is false when no rule applies.
func scopeLevelFor(name string) (Loglevel, bool) {
	rules := scopeRules.Load()
	if rules == nil {
		return DEFAULT, false
	}
	matchLevel := DEFAULT
	matchLen := -1
	for _, r := range *rules {
		if r.prefix == "" {
			if matchLen < 0 {
				matchLevel = r.level
				matchLen = 0
			}
			continue
		}
		if len(r.prefix) > matchLen && scopeMatches(name, r.prefix) {
			matchLevel = r.level
			matchLen = len(r.prefix)
		}
	}
	if matchLen < 0 {
		return DEFAULT, false
	}
	return matchLevel, true
}
