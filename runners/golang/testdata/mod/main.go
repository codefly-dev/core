package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetOutput(os.Stdout)
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
			logrus.Infof("running")
			time.Sleep(100 * time.Millisecond)
		}
	}
}
