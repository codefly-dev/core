package wool_test

import (
	"sync"
	"testing"

	"github.com/codefly-dev/core/wool"
)

func TestBufferedProcessor_ProcessConcurrentWithFlush(t *testing.T) {
	bp := wool.NewBufferedProcessor(&capture{}, 64)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 1000 {
				bp.Process(&wool.Log{Level: wool.INFO, Message: "message"})
			}
		})
	}
	bp.Flush()
	wg.Wait()
	// Repeated/concurrent-safe shutdown and post-close logging are part of the
	// contract; neither may panic or block.
	bp.Flush()
	bp.Process(&wool.Log{Level: wool.INFO, Message: "after close"})
}
