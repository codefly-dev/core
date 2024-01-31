package main

import (
	"context"
	"fmt"
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

		default:
			time.Sleep(500 * time.Millisecond)
			panic("oops!")
		}
	}
}
