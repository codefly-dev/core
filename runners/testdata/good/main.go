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
	i := 0
	for {
		select {
		case <-ctx.Done():
			fmt.Println("signal received")

		default:
			fmt.Printf("running %d\n", i)
			i++
			time.Sleep(100 * time.Millisecond)
		}
	}
}
