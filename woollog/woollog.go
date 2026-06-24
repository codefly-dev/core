// Package woollog adapts a gortk log spec to a wool logger. New returns an
// io.Writer that buffers a process's output stream, parses each line with the
// spec (gortk.LogParser), and re-emits it through wool at the parsed severity
// level — the reusable form of the per-agent log writers (postgres, redis, …).
//
// An agent supplies only a gortk.LogSpec (the prefix regex + level map) and the
// fields to surface; the buffering, line splitting, and level routing live here.
package woollog

import (
	"bytes"
	"io"
	"strings"

	"github.com/codefly-dev/core/wool"
	"github.com/mind-build/gortk"
)

// Writer parses a log stream and routes each line to a wool logger at its
// parsed level. It implements io.Writer, so it drops in wherever a process's
// output is piped to wool (runner.WithOutput(...)).
type Writer struct {
	w      *wool.Wool
	parser *gortk.LogParser
	fields []string
	buf    []byte
}

// New compiles spec and returns a Writer routing parsed lines to w. fields names
// the Record fields to attach to each entry besides the message (e.g. "pid").
func New(w *wool.Wool, spec gortk.LogSpec, fields ...string) (*Writer, error) {
	p, err := spec.Compile()
	if err != nil {
		return nil, err
	}
	return &Writer{w: w, parser: p, fields: fields}, nil
}

// MustNew is New for package-level/start-up specs that are effectively
// constants; it panics on an invalid spec.
func MustNew(w *wool.Wool, spec gortk.LogSpec, fields ...string) *Writer {
	lw, err := New(w, spec, fields...)
	if err != nil {
		panic("woollog: " + err.Error())
	}
	return lw
}

var _ io.Writer = (*Writer)(nil)

// Write buffers incoming bytes and flushes complete (newline-terminated) lines;
// a partial trailing line is held until its newline arrives (the runner may
// deliver output in arbitrary chunks).
func (lw *Writer) Write(b []byte) (int, error) {
	lw.buf = append(lw.buf, b...)
	for {
		i := bytes.IndexByte(lw.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(lw.buf[:i], "\r"))
		lw.buf = lw.buf[i+1:]
		lw.emit(line)
	}
	return len(b), nil
}

func (lw *Writer) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	rec := lw.parser.Parse(line)
	msg, _ := rec.Fields["msg"].(string)

	var fs []*wool.LogField
	for _, name := range lw.fields {
		if v, ok := rec.Fields[name].(string); ok && v != "" {
			fs = append(fs, wool.Field(name, v))
		}
	}
	switch rec.Level {
	case "fatal":
		lw.w.Fatal(msg, fs...)
	case "error":
		lw.w.Error(msg, fs...)
	case "warn":
		lw.w.Warn(msg, fs...)
	case "debug":
		lw.w.Debug(msg, fs...)
	default:
		lw.w.Info(msg, fs...)
	}
}
