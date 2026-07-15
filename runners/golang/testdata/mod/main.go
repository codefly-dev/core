package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx := context.Background()
	// Signal handling
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("signal received")
			os.Exit(0)

		default:
			fmt.Println("running")
			time.Sleep(100 * time.Millisecond)
		}
	}
}
