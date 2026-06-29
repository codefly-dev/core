package wool

import (
	"os"
	"strings"
	"sync"
)

// IsDebug returns true if the global log level is DEBUG or TRACE.
func IsDebug() bool {
	l := GlobalLogLevel()
	return l == DEBUG || l == TRACE
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

// scopeRule maps a scope-name prefix to a log level. A rule with an empty
// prefix is the catch-all (written "*" in the spec).
type scopeRule struct {
	prefix string
	level  Loglevel
}

var (
	scopeRules       []scopeRule
	scopeRulesLoaded bool
)

// SetLogScopes installs per-scope level overrides, replacing any previously set
// (including those loaded lazily from CODEFLY_LOG). The spec is a comma list of
// "scope=level" entries, e.g. "network=debug,resources=info,*=warn"; "*" is the
// catch-all. A scope matches when the .In(...) name has the rule prefix; the
// longest matching prefix wins. Unparseable entries are ignored.
func SetLogScopes(spec string) {
	lock.Lock()
	defer lock.Unlock()
	scopeRules = parseLogScopes(spec)
	scopeRulesLoaded = true
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
		if name == "*" {
			name = ""
		}
		rules = append(rules, scopeRule{prefix: name, level: level})
	}
	return rules
}

func logScopeRules() []scopeRule {
	lock.Lock()
	defer lock.Unlock()
	if !scopeRulesLoaded {
		scopeRules = parseLogScopes(os.Getenv("CODEFLY_LOG"))
		scopeRulesLoaded = true
	}
	return scopeRules
}

// scopeLevelFor returns the level override for a .In(...) scope name. The
// longest matching prefix wins; the catch-all ("*") applies when no prefix
// matches. The second return is false when no rule applies.
func scopeLevelFor(name string) (Loglevel, bool) {
	rules := logScopeRules()
	if len(rules) == 0 {
		return DEFAULT, false
	}
	matchLevel := DEFAULT
	matchLen := -1
	for _, r := range rules {
		if r.prefix == "" {
			if matchLen < 0 {
				matchLevel = r.level
				matchLen = 0
			}
			continue
		}
		if strings.HasPrefix(name, r.prefix) && len(r.prefix) > matchLen {
			matchLevel = r.level
			matchLen = len(r.prefix)
		}
	}
	if matchLen < 0 {
		return DEFAULT, false
	}
	return matchLevel, true
}
