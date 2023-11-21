package shared

import (
	"fmt"
	"os"
)

func Exit(msg string, args ...any) {
	fmt.Println(fmt.Sprintf(msg, args...))
	os.Exit(0)
}

func ExitOnFalse(b bool, msg string, args ...any) {
	logger := NewLogger("shared.ExitOnError")
	if !b {
		logger.Oops(fmt.Sprintf(msg, args...))
		os.Exit(1)
	}
}

func ExitOnError(err error, msg string, args ...any) {
	logger := NewLogger("shared.ExitOnError")
	if err != nil {
		logger.Oops("%s: %v", fmt.Sprintf(msg, args...), err)
		os.Exit(1)
	}
}

func UnexpectedExitOnError(err error, msg string, args ...any) {
	logger := NewLogger("shared.ExitOnError")
	if err != nil {
		logger.Oops("%s: %v", fmt.Sprintf(msg, args...), err)
		os.Exit(1)
	}
}
