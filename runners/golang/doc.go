// Package golang is the Go-specific runner: build, test, and run Go
// programs in any of the supported execution environments (native,
// Docker, Nix) with consistent behavior.
//
// The GoRunnerEnvironment manages module download, the binary build
// (whose reuse is the Go toolchain's own, see docs/build-cache.md), and
// process lifecycle; the package also supplies the
// agent-side helpers (agent_builder, agent_runtime, agent_test) that
// the go-grpc / go agents embed.
package golang
