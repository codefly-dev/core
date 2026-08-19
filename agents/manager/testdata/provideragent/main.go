package main

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/codefly-dev/core/agents"
	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

const (
	processGroupRoleEnv        = "CODEFLY_PROVIDER_FIXTURE_PROCESS_GROUP_ROLE"
	processGroupPIDFileEnv     = "CODEFLY_PROVIDER_FIXTURE_PROCESS_GROUP_PID_FILE"
	processGroupStoppedFileEnv = "CODEFLY_PROVIDER_FIXTURE_PROCESS_GROUP_STOPPED_FILE"
)

type server struct {
	providerv0.UnimplementedProviderServer
}

func main() {
	if os.Getenv(processGroupRoleEnv) == "descendant" {
		serveDescendant()
		return
	}
	if pidFile := os.Getenv(processGroupPIDFileEnv); pidFile != "" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), processGroupRoleEnv+"=descendant")
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			panic(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := os.Stat(pidFile); err != nil {
			panic(err)
		}
	}
	agents.Serve(agents.PluginRegistration{Provider: &server{}})
}

func serveDescendant() {
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGTERM)
	defer signal.Stop(stopping)
	if err := os.WriteFile(os.Getenv(processGroupPIDFileEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		panic(err)
	}
	<-stopping
	if err := os.WriteFile(os.Getenv(processGroupStoppedFileEnv), []byte("stopped"), 0o600); err != nil {
		panic(err)
	}
}
