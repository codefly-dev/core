package woollog

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/codefly-dev/gortk"
)

// capProc captures emitted log entries for assertions.
type capProc struct{ entries []*wool.Log }

func (c *capProc) Process(msg *wool.Log) { c.entries = append(c.entries, msg) }

func newCapturingWool(t *testing.T) (*wool.Wool, *capProc) {
	t.Helper()
	cap := &capProc{}
	ctx := context.Background()
	p := wool.New(ctx, &wool.Resource{Kind: "test", Unique: "woollog-test"}).WithLogger(cap)
	return p.Get(ctx), cap
}

var spec = gortk.LogSpec{
	LineRegex:    `^\S+ \[(?P<level>\w+)\]\s+(?P<msg>.*)$`,
	LevelMap:     map[string]string{"INFO": "info", "WARN": "warn", "ERROR": "error", "DEBUG": "debug"},
	DefaultLevel: "info",
}

func TestRoutesByLevel(t *testing.T) {
	w, cap := newCapturingWool(t)
	lw := MustNew(w, spec)

	lw.Write([]byte("2026-06-16T00:00:00Z [INFO]  core: ready\n"))
	lw.Write([]byte("2026-06-16T00:00:01Z [ERROR]  core: boom\n"))
	lw.Write([]byte("a banner line without prefix\n")) // unmatched -> info, msg = whole line

	if len(cap.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(cap.entries))
	}
	if cap.entries[0].Level != wool.INFO || cap.entries[0].Message != "core: ready" {
		t.Errorf("entry 0 wrong: level=%v msg=%q", cap.entries[0].Level, cap.entries[0].Message)
	}
	if cap.entries[1].Level != wool.ERROR || cap.entries[1].Message != "core: boom" {
		t.Errorf("entry 1 wrong: level=%v msg=%q", cap.entries[1].Level, cap.entries[1].Message)
	}
	if cap.entries[2].Level != wool.INFO || cap.entries[2].Message != "a banner line without prefix" {
		t.Errorf("unmatched line should pass through at info: %+v", cap.entries[2])
	}
}

func TestPartialLinesBuffered(t *testing.T) {
	w, cap := newCapturingWool(t)
	lw := MustNew(w, spec)
	lw.Write([]byte("2026-06-16T00:00:00Z [WAR")) // split mid-line
	if len(cap.entries) != 0 {
		t.Fatalf("partial line should not emit yet, got %d", len(cap.entries))
	}
	lw.Write([]byte("N]  almost\n"))
	if len(cap.entries) != 1 || cap.entries[0].Level != wool.WARN || cap.entries[0].Message != "almost" {
		t.Errorf("reassembled line wrong: %+v", cap.entries)
	}
}

func TestBadSpec(t *testing.T) {
	w, _ := newCapturingWool(t)
	if _, err := New(w, gortk.LogSpec{LineRegex: "("}); err == nil {
		t.Error("expected error for bad regex")
	}
}
