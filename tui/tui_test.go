package tui

import (
	"testing"

	"github.com/codefly-dev/wool"
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

func TestRenderMarkdown(t *testing.T) {
	out, err := RenderMarkdown("# Hello", "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty markdown output")
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
