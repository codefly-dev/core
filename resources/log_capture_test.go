package resources_test

import (
	"strings"
	"sync"

	"github.com/codefly-dev/core/wool"
)

// warningCapture collects what a test logs, so a test can assert on a
// breadcrumb the code leaves rather than on what it returns.

type warningCapture struct {
	mu   sync.Mutex
	logs []*wool.Log
}

func (c *warningCapture) Process(log *wool.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, log)
}

func (c *warningCapture) count(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, l := range c.logs {
		if strings.Contains(l.Message, substr) {
			n++
		}
	}
	return n
}
