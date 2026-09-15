// Command sessiondriver starts one default dependency session and hands
// teardown control to whoever spawned it. It exists so the acceptance test can
// run two sessions as genuinely independent processes: two goroutines in one
// process would share the environment SetEnvironment injects into, which is
// not the scenario the audit describes.
//
// Protocol: prints READY once the session is up, then waits for a line on
// stdin. "STOP" destroys the session and prints STOPPED.
//
// CODEFLY_TEST_SESSION_SCOPE turns on reusable mode under that naming scope,
// and makes the driver print the endpoints it resolved on the line after READY:
// several drivers sharing one warm stack all report the same endpoint, and
// drivers that each spawned their own report different ones. The name is
// namespaced because this fixture inherits the environment of whoever runs it.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/codefly-dev/core/sdk"
)

func main() {
	ctx := context.Background()
	options := []sdk.OptionFunc{sdk.WithTimeout(60 * time.Second)}
	scope := os.Getenv("CODEFLY_TEST_SESSION_SCOPE")
	if scope != "" {
		options = append(options, sdk.WithKeepRunning(), sdk.WithNamingScope(scope))
	}
	deps, err := sdk.WithDependencies(ctx, options...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessiondriver: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("READY")
	if scope != "" {
		fmt.Println(endpoints(deps))
	}

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

func endpoints(deps *sdk.Dependencies) string {
	var resolved []string
	for key, value := range deps.EnvironmentVariables() {
		if strings.HasPrefix(key, "CODEFLY__ENDPOINT__") {
			resolved = append(resolved, key+"="+value)
		}
	}
	slices.Sort(resolved)
	return strings.Join(resolved, ",")
}
