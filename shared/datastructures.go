package shared

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// SliceWriter is an io.Writer that splits its input on newlines and
// appends each completed line to Data. Safe for concurrent writers —
// the runners/base Forward fan-out spawns multiple goroutines that
// all write into the same SliceWriter, and the previous (mutex-less)
// version panicked with `slice bounds out of range` when bytes.Buffer
// raced on its internal cursor.
type SliceWriter struct {
	mu   sync.Mutex
	Data []string
	buf  bytes.Buffer
}

func NewSliceWriter() *SliceWriter {
	return &SliceWriter{
		Data: []string{},
	}
}

func (sw *SliceWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	n, err = sw.buf.Write(p)
	if err != nil {
		return n, err
	}
	for {
		line, err := sw.buf.ReadString('\n')
		if err == io.EOF {
			if line != "" {
				sw.Data = append(sw.Data, strings.TrimSpace(line))
			}
			break
		}
		if err != nil {
			return n, err
		}
		sw.Data = append(sw.Data, strings.TrimSpace(line))
	}
	return n, nil
}

func (sw *SliceWriter) Close() error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.buf.Len() > 0 {
		sw.Data = append(sw.Data, strings.TrimSpace(sw.buf.String()))
	}
	return nil
}

// Snapshot returns a copy of Data safe to read without holding the
// mutex. Call instead of accessing sw.Data directly when other
// goroutines may still be writing.
func (sw *SliceWriter) Snapshot() []string {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	out := make([]string, len(sw.Data))
	copy(out, sw.Data)
	return out
}

type SignalWriter struct {
	Writer  io.Writer
	signal  chan struct{}
	hasData bool
}

func NewSignalWriter(writer io.Writer) *SignalWriter {
	return &SignalWriter{
		hasData: false,
		Writer:  writer,
		signal:  make(chan struct{}, 1),
	}
}

func (sw *SignalWriter) Write(p []byte) (n int, err error) {
	n, err = sw.Writer.Write(p)
	if !sw.hasData && n > 0 {
		select {
		case sw.signal <- struct{}{}:
		default:
		}
		sw.hasData = true
	}
	return
}

func (sw *SignalWriter) Signal() <-chan struct{} {
	return sw.signal
}
