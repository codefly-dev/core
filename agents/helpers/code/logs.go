package code

import (
	"bufio"
	"fmt"
	"io"
)

func ProcessLogs(r io.ReadCloser, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	var firstErr error
	for scanner.Scan() {
		// Scanner.Text() strips the trailing newline; re-add it, otherwise all
		// forwarded log lines concatenate into a single unreadable line.
		if _, err := w.Write([]byte(scanner.Text() + "\n")); err != nil {
			firstErr = fmt.Errorf("writing log line: %w", err)
			break
		}
	}

	if firstErr == nil {
		if err := scanner.Err(); err != nil {
			firstErr = fmt.Errorf("reading logs: %w", err)
		}
	}

	if err := r.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("closing log reader: %w", err)
	}

	return firstErr
}
