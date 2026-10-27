package wool_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// These tests drive the real Console, which writes to os.Stdout, so they swap
// os.Stdout for a pipe and stay serial (no t.Parallel): the state they mutate —
// stdout, the global level, the scope rules — is process-wide.

// consoleLines runs emit with os.Stdout redirected and returns the printed lines.
func consoleLines(t *testing.T, emit func()) []string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	read := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		read <- b
	}()
	emit()
	require.NoError(t, w.Close())
	out := <-read
	require.NoError(t, r.Close())

	var lines []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSuffix(line, "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// atLevels pins the global level and the scope rules for one test.
func atLevels(t *testing.T, global wool.Loglevel, scopes string) {
	t.Helper()
	previous := wool.GlobalLogLevel()
	t.Cleanup(func() {
		wool.SetGlobalLogLevel(previous)
		wool.SetLogScopes("")
	})
	wool.SetGlobalLogLevel(global)
	wool.SetLogScopes(scopes)
}

// consoles are the two ways a process ends up printing through a Console: the
// fallback every wool.Get gets with no provider, and Provider.WithConsole at the
// global level.
var consoles = map[string]func() *wool.Wool{
	"fallback": func() *wool.Wool {
		wool.SetFallbackLogger(nil)
		return wool.Get(context.Background())
	},
	"WithConsole": func() *wool.Wool {
		p := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "console"}).
			WithConsole(wool.INFO)
		return wool.Get(p.Inject(context.Background()))
	},
}

func requirePrinted(t *testing.T, lines []string, want ...string) {
	t.Helper()
	require.Len(t, lines, len(want), "printed: %q", lines)
	for i, msg := range want {
		require.Contains(t, lines[i], msg)
	}
}

func TestConsole_ScopeRaisesAboveGlobalLevel(t *testing.T) {
	for name, get := range consoles {
		t.Run(name, func(t *testing.T) {
			atLevels(t, wool.INFO, "component=trace")
			base := get()
			lines := consoleLines(t, func() {
				base.In("component.method").Trace("raised")
				base.In("other.method").Trace("not raised")
				base.In("other.method").Info("global")
			})
			requirePrinted(t, lines, "raised", "global")
		})
	}
}

func TestConsole_CatchAllStillQuiets(t *testing.T) {
	for name, get := range consoles {
		t.Run(name, func(t *testing.T) {
			atLevels(t, wool.INFO, "component=trace,*=warn")
			base := get()
			lines := consoleLines(t, func() {
				base.In("component.method").Trace("raised")
				base.In("other.method").Info("quieted")
				base.In("other.method").Warn("loud")
			})
			requirePrinted(t, lines, "raised", "loud")
		})
	}
}

// A threshold a caller passes to WithConsole is kept: it quiets the global
// level, never raises above it, and gives way to a scope rule.
func TestConsole_WithConsoleThreshold(t *testing.T) {
	provider := func(lvl wool.Loglevel) *wool.Wool {
		p := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "threshold"}).
			WithConsole(lvl)
		return wool.Get(p.Inject(context.Background()))
	}

	t.Run("quiets the global level", func(t *testing.T) {
		atLevels(t, wool.DEBUG, "")
		base := provider(wool.WARN)
		lines := consoleLines(t, func() {
			base.In("other.method").Info("below threshold")
			base.In("other.method").Warn("at threshold")
		})
		requirePrinted(t, lines, "at threshold")
	})

	t.Run("never louder than the global level", func(t *testing.T) {
		atLevels(t, wool.INFO, "")
		base := provider(wool.TRACE)
		lines := consoleLines(t, func() {
			base.In("other.method").Debug("below global")
			base.In("other.method").Info("at global")
		})
		requirePrinted(t, lines, "at global")
	})

	t.Run("gives way to a scope rule", func(t *testing.T) {
		atLevels(t, wool.INFO, "component=trace")
		base := provider(wool.WARN)
		lines := consoleLines(t, func() {
			base.In("component.method").Trace("raised")
			base.In("other.method").Info("below threshold")
		})
		requirePrinted(t, lines, "raised")
	})
}
