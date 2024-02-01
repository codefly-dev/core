package runners

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// WaitForPortUnbound waits for the portMapping to be unbound
func WaitForPortUnbound(ctx context.Context, port int) error {
	// Create a new context that will be done after 5 seconds
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("waited for portMapping to unbound but timed out after 5 seconds")
		default:
			_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
			if err != nil {
				if strings.Contains(err.Error(), "connection refused") {
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			} else {
				return nil

			}
		}
	}
}

func IsFreePort(port int) bool {
	_, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	return err == nil
}
