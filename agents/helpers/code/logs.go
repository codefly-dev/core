package code

import (
	"bufio"
	"io"
)

func ProcessLogs(r io.ReadCloser, w io.Writer) {
	// TODO: Logging error strategy
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// Scanner.Text() strips the trailing newline; re-add it, otherwise all
		// forwarded log lines concatenate into a single unreadable line.
		_, _ = w.Write([]byte(scanner.Text() + "\n"))
	}

	if err := scanner.Err(); err != nil {
		_, _ = w.Write([]byte(err.Error()))
	}

	_ = r.Close()
}
