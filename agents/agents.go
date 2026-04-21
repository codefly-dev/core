package agents

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"

	"google.golang.org/grpc"
)

// ProtocolVersion is used for future-proofing the stdout handshake.
const ProtocolVersion = 1

// PluginRegistration holds the gRPC servers a plugin wants to expose.
// All registration is handled by core -- plugins never import grpc directly.
//
// Plugins implement the capabilities they need:
//   - Infrastructure (redis, postgres): Agent + Runtime
//   - Application (go-grpc, python-fastapi): Agent + Runtime + Builder + Tooling
//   - Tooling-only (go-analyzer): Agent + Tooling
//
// Separation of concerns:
//   - Runtime: service lifecycle (Load/Init/Start/Stop/Destroy)
//   - Builder: Docker build + k8s deploy + scaffolding
//   - Code: file/git/LSP operations (deprecated — use Tooling for language-specific ops)
//   - Tooling: language-specific analysis (LSP, callgraph, fix, deps, build/test/lint)
type PluginRegistration struct {
	Agent   agentv0.AgentServer
	Runtime runtimev0.RuntimeServer
	Builder builderv0.BuilderServer
	Code    codev0.CodeServer           // Deprecated: use Tooling for language-specific operations.
	Tooling toolingv0.ToolingServer     // Language analysis: LSP, callgraph, fix, deps, build/test/lint.
}

// agentRPCInterceptor logs incoming RPCs with method name and duration
// to stderr (which is captured by the gateway's ring buffer).
// High-frequency polling RPCs (Information) are suppressed unless they error.
//
// In addition to the per-line stderr log, every call updates the
// in-process latency histogram (RPCStats). Callers can read it via
// SnapshotRPCStats — useful for daemons that want to expose p50/p99
// per RPC without standing up a separate metrics endpoint.
func agentRPCInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)

		method := info.FullMethod
		if idx := strings.LastIndex(method, "/"); idx >= 0 {
			method = method[idx+1:]
		}

		recordRPCDuration(method, dur, err)

		if method == "Information" && err == nil {
			return resp, err
		}

		errStr := ""
		if err != nil {
			errStr = " error=" + err.Error()
		}
		fmt.Fprintf(os.Stderr, "[agent] %s %dms%s\n", method, dur.Milliseconds(), errStr)
		return resp, err
	}
}

// rpcStats keeps per-method counters + a running histogram. The
// implementation is intentionally simple: we accumulate count/sum/max
// and bucket counts. No external metrics SDK to keep the agent binary
// small. Daemons that want richer telemetry can wrap the snapshot.
//
// Thread-safe via a single mutex — RPC throughput on a single agent is
// modest (Test/Build/Init are bursty but never high-rate), so contention
// is not a concern.
type rpcMethodStats struct {
	Count   uint64        // total invocations
	Errors  uint64        // count where err != nil
	Sum     time.Duration // sum of all durations
	Max     time.Duration // longest call seen
	Buckets [9]uint64     // histogram, see latencyBuckets
}

// latencyBuckets are upper-inclusive ms boundaries. Final bucket is
// implicit (everything over 30s).
var latencyBuckets = [9]time.Duration{
	1 * time.Millisecond,
	5 * time.Millisecond,
	25 * time.Millisecond,
	100 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	1 * time.Hour, // overflow sink
}

var (
	rpcStatsMu sync.Mutex
	rpcStats   = make(map[string]*rpcMethodStats)
)

func recordRPCDuration(method string, dur time.Duration, err error) {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()

	s, ok := rpcStats[method]
	if !ok {
		s = &rpcMethodStats{}
		rpcStats[method] = s
	}
	s.Count++
	s.Sum += dur
	if err != nil {
		s.Errors++
	}
	if dur > s.Max {
		s.Max = dur
	}
	for i, b := range latencyBuckets {
		if dur <= b {
			s.Buckets[i]++
			return
		}
	}
}

// SnapshotRPCStats returns a deep copy of the current per-method stats.
// Safe to call concurrently with live RPCs.
func SnapshotRPCStats() map[string]rpcMethodStats {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()
	out := make(map[string]rpcMethodStats, len(rpcStats))
	for k, v := range rpcStats {
		out[k] = *v
	}
	return out
}

// ResetRPCStats clears all counters. Test-only — production agents
// should never call this (loses telemetry).
func ResetRPCStats() {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()
	rpcStats = make(map[string]*rpcMethodStats)
}

// Serve starts a gRPC server on a random local port, registers the
// plugin's services, signals the port to the CLI via stdout, and
// blocks until the process is terminated.
//
// Plugin binaries call this from main():
//
//	agents.Serve(agents.PluginRegistration{
//	    Agent:   svc,
//	    Runtime: NewRuntime(),
//	    Builder: NewBuilder(),
//	    Code:    NewCode(),
//	})
func Serve(reg PluginRegistration) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents.Serve: cannot listen: %v\n", err)
		os.Exit(1)
	}

	s := grpc.NewServer(
		grpc.UnaryInterceptor(agentRPCInterceptor()),
	)

	if reg.Agent != nil {
		agentv0.RegisterAgentServer(s, reg.Agent)
	}
	if reg.Runtime != nil {
		runtimev0.RegisterRuntimeServer(s, reg.Runtime)
	}
	if reg.Builder != nil {
		builderv0.RegisterBuilderServer(s, reg.Builder)
	}
	if reg.Code != nil {
		codev0.RegisterCodeServer(s, reg.Code)
	}
	if reg.Tooling != nil {
		toolingv0.RegisterToolingServer(s, reg.Tooling)
	}

	// Signal the port to the parent (CLI) using the protocol: "VERSION|PORT\n"
	port := lis.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(os.Stdout, "%d|%d\n", ProtocolVersion, port)

	// Graceful shutdown on SIGTERM/SIGINT.
	//
	// Critical: call the Runtime's Stop FIRST so spawned child processes
	// (user binaries, Docker containers, Nix shells) get torn down. Without
	// this, killing the parent codefly leaks every child as a PPID=1 orphan
	// because GracefulStop only stops the gRPC server — it does NOT touch
	// whatever the agent has running.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		if reg.Runtime != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = reg.Runtime.Stop(stopCtx, &runtimev0.StopRequest{})
			cancel()
		}
		s.GracefulStop()
	}()

	if err := s.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "agents.Serve: %v\n", err)
		os.Exit(1)
	}
}
