package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/codefly-dev/core/wool"
	"github.com/fatih/color"
)

func TestLogViewCopySafety(t *testing.T) {
	lv := NewLogView()

	// Simulate bubbletea's value-copy behavior: copy the struct, mutate the
	// copy, then call View. strings.Builder panics if copied after first
	// write -- using *strings.Builder prevents this.
	for i := 0; i < 100; i++ {
		copy := lv
		copy.AppendText("line")
		_ = copy.View()
	}

	lv.AppendText("after copies")
	out := lv.View()
	if out == "" {
		t.Fatal("expected non-empty View after appending text")
	}
}

func TestLogViewAppendLog(t *testing.T) {
	lv := NewLogView()
	lv.appendLog(ServiceLogMsg{Level: wool.INFO, Source: "test/svc", Message: "hello"})
	lv.appendLog(ServiceLogMsg{Level: wool.ERROR, Source: "test/svc", Message: "boom"})
	lv.appendLog(ServiceLogMsg{Level: wool.WARN, Source: "test/svc", Message: "careful"})
	lv.appendLog(ServiceLogMsg{Level: wool.DEBUG, Source: "test/svc", Message: "debug"})
	lv.appendLog(ServiceLogMsg{Level: wool.TRACE, Source: "test/svc", Message: "trace"})
	lv.appendLog(ServiceLogMsg{Level: wool.FORWARD, Source: "test/svc", Message: "fwd"})

	content := lv.lines.String()
	if content == "" {
		t.Fatal("expected non-empty lines after appending logs")
	}
}

func TestFormatServiceLogLinesTrimsForwardNewlines(t *testing.T) {
	lines := formatServiceLogLines(ServiceLogMsg{
		Level:   wool.FORWARD,
		Source:  "infra/postgres",
		Message: "database ready\n\n",
	}, 80)

	if len(lines) != 1 {
		t.Fatalf("expected one rendered line, got %d: %#v", len(lines), lines)
	}
	if lines[0] != "infra/postgres   > database ready" {
		t.Fatalf("unexpected rendered line: %q", lines[0])
	}
}

func TestFormatServiceLogLinesWrapsWithContinuationIndent(t *testing.T) {
	lines := formatServiceLogLines(ServiceLogMsg{
		Level:   wool.FORWARD,
		Source:  "infra/neo4j",
		Message: "This instance is ServerId{257a2499} (257a2499-28b2-4325-b1bf-b5)",
	}, 52)

	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines, got %#v", lines)
	}
	if lines[0] != "infra/neo4j      > This instance is" {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if lines[1] != "                   ServerId{257a2499}" {
		t.Fatalf("unexpected continuation line: %q", lines[1])
	}
}

func TestRenderServiceLogLineColorsForwardLogsByService(t *testing.T) {
	restoreColorEnv(t)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CODEFLY_COLOR", "")
	configureColorProfile()

	line := renderServiceLogLine(ServiceLogMsg{
		Level:  wool.FORWARD,
		Source: "infra/postgres",
	}, "infra/postgres   > database ready")
	if !strings.Contains(line, "\x1b[") {
		t.Fatalf("expected forwarded service log to contain ANSI color, got %q", line)
	}

	view := NewLogView()
	view.SetSize(80, 10)
	view.appendLog(ServiceLogMsg{
		Level:   wool.FORWARD,
		Source:  "infra/postgres",
		Message: "database ready",
	})
	if !strings.Contains(view.View(), "\x1b[") {
		t.Fatalf("expected viewport to preserve ANSI color, got %q", view.View())
	}
}

func TestServiceRendererDeterministic(t *testing.T) {
	r1 := ServiceRenderer("mod/svc")
	r2 := ServiceRenderer("mod/svc")

	out1 := r1("hello")
	out2 := r2("hello")
	if out1 != out2 {
		t.Fatalf("same unique should produce same output: %q != %q", out1, out2)
	}

	r3 := ServiceRenderer("other/svc")
	out3 := r3("hello")
	// Different unique may produce different output (not guaranteed, but
	// verify no panic).
	_ = out3
}

func TestServiceFocusRendererNoPanic(t *testing.T) {
	fn := ServiceFocusRenderer("mod/svc")
	out := fn("test text")
	if out == "" {
		t.Fatal("expected non-empty focus render output")
	}

	fn2 := ServiceFocusRenderer("single")
	out2 := fn2("test text")
	if out2 == "" {
		t.Fatal("expected non-empty focus render for single-part unique")
	}
}

func TestRenderHelpers(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
	}{
		{"RenderHeader1", func() string { return RenderHeader(1, "title") }},
		{"RenderHeader2", func() string { return RenderHeader(2, "title") }},
		{"RenderHeaderDefault", func() string { return RenderHeader(99, "title") }},
		{"RenderWarning", func() string { return RenderWarning("warn") }},
		{"RenderTrace", func() string { return RenderTrace("trace") }},
		{"RenderDebug", func() string { return RenderDebug("debug") }},
		{"RenderInfo", func() string { return RenderInfo("info") }},
		{"RenderError", func() string { return RenderError("error") }},
		{"RenderErrorDetail", func() string { return RenderErrorDetail("detail") }},
		{"RenderFocus", func() string { return RenderFocus("focus") }},
		{"RenderWithMargin", func() string { return RenderWithMargin("margin") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.fn()
			if out == "" {
				t.Fatalf("%s returned empty string", tt.name)
			}
		})
	}
}

func TestConfigureColorProfileDefaultsToCodeflyColorPolicy(t *testing.T) {
	restoreColorEnv(t)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CODEFLY_COLOR", "")

	configureColorProfile()
	out := lipgloss.NewStyle().Foreground(ColorSuccess).Render("ok")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected Codefly default to keep colors despite NO_COLOR, got %q", out)
	}
}

func TestConfigureColorProfileCanDisableColors(t *testing.T) {
	restoreColorEnv(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CODEFLY_COLOR", "never")

	configureColorProfile()
	out := lipgloss.NewStyle().Foreground(ColorSuccess).Render("ok")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("expected CODEFLY_COLOR=never to disable ANSI color, got %q", out)
	}
	fatihOut := color.New(color.FgGreen).Sprint("ok")
	if strings.Contains(fatihOut, "\x1b[") {
		t.Fatalf("expected CODEFLY_COLOR=never to disable fatih/color output, got %q", fatihOut)
	}
}

func restoreColorEnv(t *testing.T) {
	t.Helper()
	codeflyColor, hadCodeflyColor := os.LookupEnv("CODEFLY_COLOR")
	noColor, hadNoColor := os.LookupEnv("NO_COLOR")
	t.Cleanup(func() {
		if hadCodeflyColor {
			_ = os.Setenv("CODEFLY_COLOR", codeflyColor)
		} else {
			_ = os.Unsetenv("CODEFLY_COLOR")
		}
		if hadNoColor {
			_ = os.Setenv("NO_COLOR", noColor)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
		configureColorProfile()
	})
}

func TestRenderMarkdown(t *testing.T) {
	out, err := RenderMarkdown("# Hello", "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty markdown output")
	}
}

func TestMilestoneLinesShareOneForm(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"state", ServiceStateMsg{Service: "mind/mind", State: StateStarting}, ">> mind/mind: Starting"},
		{"ready", ServiceReadyMsg{Service: "mind/mind"}, ">> mind/mind: Running"},
		{"ready with port", ServiceReadyMsg{Service: "mind/mind", Port: 6690}, ">> mind/mind: Running on :6690"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newServiceRunnerModel("mind/mind")
			updated, _ := m.Update(tc.msg)
			content := updated.(ServiceRunnerModel).logView.lines.String()
			if !strings.Contains(content, tc.want) {
				t.Fatalf("milestone line %q not found in log content: %q", tc.want, content)
			}
		})
	}
}

func TestServiceStateString(t *testing.T) {
	states := []ServiceState{
		StateLoading, StateInitializing, StateStarting, StateRunning,
		StateTesting, StateStopping, StateStopped, StateFailed,
	}
	for _, s := range states {
		if s.String() == "Unknown" {
			t.Fatalf("state %d should not be Unknown", s)
		}
	}
	if ServiceState(99).String() != "Unknown" {
		t.Fatal("undefined state should be Unknown")
	}
}
