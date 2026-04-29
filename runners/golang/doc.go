// Package golang is the Go-specific runner: build, test, and run Go
// programs in any of the supported execution environments (native,
// Docker, Nix) with consistent behavior.
//
// The GoRunnerEnvironment manages module download, binary build with
// caching, and process lifecycle; the package also supplies the
// agent-side helpers (agent_builder, agent_runtime, agent_test) that
// the go-grpc / go agents embed.
package golang
