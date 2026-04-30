// Package mcp transcodes between the codefly toolbox v0 contract
// (Connect-RPC over gRPC) and the Model Context Protocol (JSON-RPC
// 2.0 over stdio). External AI clients that speak MCP — Claude
// Desktop, Cursor, etc. — connect to a transcoded toolbox the same
// way they connect to any other MCP server.
//
// This is the Phase 2 transcoder: thin, stateless, no protocol
// translation logic beyond name/argument mapping. Every primitive
// in MCP has a 1:1 correspondent in the toolbox contract because we
// designed the contract that way:
//
//	MCP                          ↔  toolbox v0
//	────────────────────────────────────────────────────────
//	tools/list                   ↔  ListTools
//	tools/call                   ↔  CallTool
//	resources/list               ↔  ListResources
//	resources/read               ↔  ReadResource
//	prompts/list                 ↔  ListPrompts
//	prompts/get                  ↔  GetPrompt
//	initialize → serverInfo      ↔  Identity
//
// Transport: JSON-RPC 2.0 over a single io.Reader / io.Writer pair,
// usually os.Stdin / os.Stdout. Each frame is a JSON object on its
// own line — same wire format used by the existing cli/pkg/mcp
// server, so an MCP client that talks to one talks to the other.
//
// Why this lives in core/toolbox/mcp/ and not cli/pkg/mcp/: the
// transcoder takes a toolboxv0.ToolboxClient. That client may point
// at an in-process toolbox (for tests, for embedded use cases) or
// at a spawned plugin (host.Plugin.Client()). Keeping the
// transcoder in core means any binary — the codefly CLI, a future
// `codefly toolbox mcp <name>` subcommand, or a third-party host —
// can wrap a toolbox in MCP without depending on cli/.
package mcp
