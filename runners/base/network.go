package base

import (
	"context"
	"fmt"
	"net"
	"time"
)

// WaitForPortUnbound waits for the portMappings to be unbound
func WaitForPortUnbound(ctx context.Context, port int) error {
	// Create a new context that will be done after 5 seconds
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("waited for portMappings to unbound but timed out after 5 seconds")
		default:
			if IsFreePort(port) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func IsFreePort(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// If the function returns an error, the port is not available
		return false
	}

	// Be sure to close the listener if the function does not return an error
	listener.Close()
	return true
}

// FindFreePort asks the OS to assign a free TCP port and returns it.
func FindFreePort() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("cannot find free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port, nil
}
