package shared

import (
	"io"
)

type SliceWriter struct {
	Data []string
}

func NewSliceWriter() *SliceWriter {
	return &SliceWriter{
		Data: []string{},
	}
}

func (sw *SliceWriter) Write(p []byte) (n int, err error) {
	sw.Data = append(sw.Data, string(p))
	return len(p), nil
}

func (sw *SliceWriter) Close() error {
	return nil
}

type SignalWriter struct {
	Writer  io.Writer
	signal  chan bool
	hasData bool
}

func NewSignalWriter(writer io.Writer) *SignalWriter {
	return &SignalWriter{
		hasData: false,
		Writer:  writer,
		signal:  make(chan bool, 1),
	}
}

func (sw *SignalWriter) Write(p []byte) (n int, err error) {
	n, err = sw.Writer.Write(p)
	if !sw.hasData && n > 0 {
		// Non-blocking send to signal channel
		select {
		case sw.signal <- true:
		default:
		}
		sw.hasData = true
	}
	return
}

func (sw *SignalWriter) Signal() <-chan bool {
	return sw.signal
}
