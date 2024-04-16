package base

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"sync"
)

func ForwardLogs(ctx context.Context, wg *sync.WaitGroup, reader io.Reader, writer io.Writer) {
	wg.Add(1)
	r := bufio.NewReader(reader)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				output, err := r.ReadBytes('\n')
				if err != nil {
					if err == io.EOF {
						return
					}
					//_, _ = writer.Write([]byte(err.Error()))
					continue
				}

				_, _ = writer.Write(bytes.TrimSpace(output))
			}
		}
	}()
}

func Forward(ctx context.Context, reader io.Reader, writer io.Writer) {
	r := bufio.NewReader(reader)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				output, err := r.ReadBytes('\n')
				if err != nil {
					if err == io.EOF {
						return
					}
					continue
				}

				_, _ = writer.Write(bytes.TrimSpace(output))
			}
		}
	}()
}
