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

/*
#include <stdlib.h>
#include <string.h>

char* getString() {
    return "Hello from C!";
}
*/
import "C"

func nothing() {
	str := C.getString()
	goStr := C.GoString(str)
	fmt.Println(goStr)
}

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
			logrus.Infof("running")
			time.Sleep(100 * time.Millisecond)
		}
	}
}
