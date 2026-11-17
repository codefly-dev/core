// A stand-in for a store agent: its Init claims a native state directory for
// the run it is told about and starts a server there that does not die with
// it, as a database agent does, and its Stop stops that server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/codefly-dev/core/agents"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/runstate"
)

type runtime struct {
	runtimev0.UnimplementedRuntimeServer
	hold *runstate.Hold
}

func (r *runtime) Load(context.Context, *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	return &runtimev0.LoadResponse{}, nil
}

func (r *runtime) Init(context.Context, *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	dir := os.Getenv("STOREAGENT_DIR")
	run := runstate.Run{Workspace: "/work/shop"}
	if os.Getenv("STOREAGENT_DISPOSABLE") == "true" {
		run.NamingScope, run.Disposable = "invabcdefgh", true
	}
	hold, err := runstate.Claim(dir, run)
	if err != nil {
		return nil, err
	}
	// Bounded, so that a failed test cannot leave it behind for good.
	server := exec.Command("sleep", "120")
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := server.Start(); err != nil {
		return nil, err
	}
	go func() { _ = server.Wait() }()
	if err := hold.Track(server.Process.Pid); err != nil {
		return nil, err
	}
	r.hold = hold
	report := fmt.Sprintf("%d %d\n", os.Getpid(), server.Process.Pid)
	if err := os.WriteFile(os.Getenv("STOREAGENT_REPORT"), []byte(report), 0o600); err != nil {
		return nil, err
	}
	return &runtimev0.InitResponse{}, nil
}

// Stop stops the server, unless the agent is one whose Stop keeps it.
func (r *runtime) Stop(ctx context.Context, _ *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	if r.hold == nil || os.Getenv("STOREAGENT_STOP_KEEPS_SERVER") == "true" {
		return &runtimev0.StopResponse{}, nil
	}
	if _, err := r.hold.StopTracked(ctx, runstate.StopOptions{Grace: 3 * time.Second}); err != nil {
		return nil, err
	}
	return &runtimev0.StopResponse{}, nil
}

func main() {
	agents.Serve(agents.PluginRegistration{Runtime: &runtime{}})
}
