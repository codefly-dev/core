package shared

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSignalWriterSupportsConcurrentWriters(t *testing.T) {
	var output bytes.Buffer
	w := NewSignalWriter(&output)

	const writers = 64
	var group sync.WaitGroup
	group.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer group.Done()
			if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
				t.Errorf("write: %v", err)
			}
		}(i)
	}
	group.Wait()

	select {
	case <-w.Signal():
	default:
		t.Fatal("first-data signal was not delivered")
	}
	if got := strings.Count(output.String(), "\n"); got != writers {
		t.Fatalf("output contains %d lines, want %d", got, writers)
	}
}
