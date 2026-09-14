// Command sessiondriver starts one default dependency session and hands
// teardown control to whoever spawned it. It exists so the acceptance test can
// run two sessions as genuinely independent processes: two goroutines in one
// process would share the environment SetEnvironment injects into, which is
// not the scenario the audit describes.
//
// Protocol: prints READY once the session is up, then waits for a line on
// stdin. "STOP" destroys the session and prints STOPPED.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codefly-dev/core/sdk"
)

func main() {
	ctx := context.Background()
	deps, err := sdk.WithDependencies(ctx, sdk.WithTimeout(60*time.Second))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessiondriver: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("READY")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "STOP" {
			continue
		}
		if err := deps.Destroy(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "sessiondriver: destroy: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("STOPPED")
		return
	}
}
