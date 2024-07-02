package shared

import (
	"bytes"
	"io"
	"strings"
)

type SliceWriter struct {
	Data []string
	buf  bytes.Buffer
}

func NewSliceWriter() *SliceWriter {
	return &SliceWriter{
		Data: []string{},
	}
}

func (sw *SliceWriter) Write(p []byte) (n int, err error) {
	n, err = sw.buf.Write(p)
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
	if sw.buf.Len() > 0 {
		sw.Data = append(sw.Data, strings.TrimSpace(sw.buf.String()))
	}
	return nil
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
