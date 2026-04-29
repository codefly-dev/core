// Package agents provides the framework for codefly service agents:
// gRPC servers that the CLI spawns as subprocesses to handle the
// service lifecycle (Load → Init → Start → Stop → Destroy) and
// the build / deploy / code-analysis flows.
//
// Every codefly agent (go-grpc, postgres, vault, redis, nextjs, …)
// implements a subset of these capabilities by registering server
// types from sub-packages:
//
//	agentv0.AgentServer       — identity + capabilities (always required)
//	runtimev0.RuntimeServer   — service-process lifecycle
//	builderv0.BuilderServer   — Docker build, k8s deploy, scaffolding
//	codev0.CodeServer         — file/git/LSP operations
//	toolingv0.ToolingServer   — language-specific analysis
//
// The Serve function binds a gRPC listener (default 127.0.0.1:0;
// override via CODEFLY_AGENT_BIND_ADDR), registers every server the
// agent declared in its PluginRegistration, exposes a standard
// grpc.health.v1 endpoint, and prints "<protocol-version>|<port>" on
// stdout so the parent CLI knows where to dial. SIGTERM/SIGINT
// trigger a graceful shutdown that fans out through Runtime.Stop
// before the gRPC server exits.
//
// Sub-packages:
//
//	manager   — CLI-side. Spawns agent processes, connects, dispatches RPCs.
//	services  — base types agents embed (RuntimeServer, BuilderServer, etc).
//	helpers   — shared helpers (Docker, code analysis).
//	communicate — interactive Q&A protocol (used by Create flow).
package agents
