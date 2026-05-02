// Package mcprev is the reverse-direction MCP transcoder: wraps an
// MCP server (speaking JSON-RPC over stdio) and exposes it as a
// codefly Toolbox gRPC server.
//
// Why: the broader MCP ecosystem already ships dozens of servers
// (Anthropic-published, community). Without this adapter, using one
// with codefly would require rewriting it as a native Toolbox
// plugin. With it, any MCP server is a codefly toolbox for free —
// the host gets the spawn / sandbox / policy / routing layer, the
// LLM caller sees a normal Toolbox.
//
// Mirror of core/toolbox/mcp, which goes the other direction
// (Toolbox gRPC → MCP JSON-RPC, the existing path Mind uses).
//
// Lifecycle: ReverseTranscoder owns the stdio pair you give it
// (typically the spawned MCP server's stdin/stdout pipes). Call
// Initialize once before any other RPC; the handshake also
// negotiates the protocol version and reads the server's
// advertised name+version into Identity().
package mcprev

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
)

// JSONRPCVersion is the pinned JSON-RPC field value, per spec.
const JSONRPCVersion = "2.0"

// MCPProtocolVersion is the MCP revision we advertise on initialize.
// Aligned with the forward transcoder for consistency.
const MCPProtocolVersion = "2024-11-05"

// ReverseTranscoder wraps an MCP server's stdio and serves the
// codefly Toolbox gRPC contract on top.
//
// Construct via New, then call Initialize before exposing the
// server through agents.Serve. After Initialize, the transcoder
// reads server identity (name, version) and is ready to relay
// tools/list and tools/call.
//
// Concurrency: the underlying JSON-RPC stream is a single
// request/response channel — the transcoder serializes calls via
// callMu so the LLM-side gRPC server can multiplex without racing
// the wire.
type ReverseTranscoder struct {
	*registry.Base

	// stdin / stdout pipe pair to the spawned MCP server.
	// Caller owns the process lifecycle.
	stdin   io.Writer
	stdout  *bufio.Reader
	closer  io.Closer // optional combined closer for both pipes
	callMu  sync.Mutex
	idCount atomic.Int64

	// Filled by Initialize.
	serverName    string
	serverVersion string
	tools         []*registry.ToolDefinition
}

// New constructs a ReverseTranscoder over the given stdio pair. The
// caller is responsible for spawning the MCP server process and
// passing its stdin/stdout. closer (optional) is invoked when the
// transcoder is torn down — typically the cmd.Wait + pipe close.
func New(stdin io.Writer, stdout io.Reader, closer io.Closer) *ReverseTranscoder {
	r := &ReverseTranscoder{
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 64*1024),
		closer: closer,
	}
	r.Base = registry.NewBase(r)
	return r
}

// Initialize performs the MCP initialize handshake and caches the
// remote server's identity + tool list. Must be called before
// exposing the transcoder via agents.Serve. Re-running it after a
// server crash is safe — state is replaced, not appended.
func (r *ReverseTranscoder) Initialize(ctx context.Context) error {
	// MCP handshake: initialize → initialized notification → tools/list.
	initParams := map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "codefly-mcprev",
			"version": "0.1.0",
		},
	}
	initResp, err := r.call(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	r.parseServerInfo(initResp)

	// Per MCP spec the client MUST send `notifications/initialized`
	// after a successful initialize before any further requests.
	if err := r.notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	// Pre-fetch the tool catalog. MCP servers don't push tool changes
	// without an explicit notification (notifications/tools/list_changed)
	// — supporting hot updates is a future enhancement.
	listResp, err := r.call(ctx, "tools/list", nil)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	r.tools = parseMCPToolList(listResp, r.callTool)
	return nil
}

// Close tears down the underlying stdio pipes / subprocess if a
// closer was supplied at construction. Idempotent — closer is
// guarded by callMu so concurrent Close from a shutdown signal +
// gRPC handler doesn't race.
func (r *ReverseTranscoder) Close() error {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	if r.closer == nil {
		return nil
	}
	err := r.closer.Close()
	r.closer = nil
	return err
}

// --- Identity + Tools ----------------------------------------

// Identity returns the wrapped MCP server's identity. Name and
// version come from the server's initialize response; description
// is generic since MCP doesn't expose a server-level description
// field (only per-tool ones).
func (r *ReverseTranscoder) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           r.serverName,
		Version:        r.serverVersion,
		Description:    "MCP server wrapped as a codefly toolbox via mcprev.",
		SandboxSummary: "inherits the wrapper's sandbox; the MCP subprocess runs inside it",
	}, nil
}

// Tools returns the tool catalog cached at Initialize time. The
// embedded registry.Base then projects this into ListTools /
// ListToolSummaries / DescribeTool / CallTool — same shape any
// native codefly toolbox uses.
func (r *ReverseTranscoder) Tools() []*registry.ToolDefinition {
	return r.tools
}

// callTool is the Handler bound on every ToolDefinition we generate
// from the MCP server's tool list. Translates a CallToolRequest
// into MCP's tools/call shape, sends, and converts the reply back.
func (r *ReverseTranscoder) callTool(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := map[string]any{}
	if req.GetArguments() != nil {
		args = req.GetArguments().AsMap()
	}
	params := map[string]any{
		"name":      req.GetName(),
		"arguments": args,
	}
	result, err := r.call(ctx, "tools/call", params)
	if err != nil {
		return &toolboxv0.CallToolResponse{Error: fmt.Sprintf("mcp tools/call: %v", err)}
	}
	return mcpResultToCallResponse(result)
}

// --- JSON-RPC plumbing ---------------------------------------

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// call sends a JSON-RPC request and reads the matching response.
// Serialized via callMu — MCP stdio is a single-channel transport,
// concurrent writers would interleave bytes; concurrent readers
// would race on the response routing. The host's gRPC server is
// fundamentally concurrent, so this serialization is load-bearing.
//
// ctx cancellation is best-effort: a request already on the wire
// can't be cancelled (MCP has no cancellation message). We can
// only refuse to wait for the reply.
func (r *ReverseTranscoder) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	r.callMu.Lock()
	defer r.callMu.Unlock()

	id := r.idCount.Add(1)
	req := jsonRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	encoded = append(encoded, '\n')

	if _, err := r.stdin.Write(encoded); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read until we see our id. Servers can interleave notifications
	// (no id) or other requests (we ignore them — we're a client of
	// the MCP server, not its peer). Loop until our reply lands.
	type readResult struct {
		resp *jsonRPCResponse
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		for {
			line, err := r.stdout.ReadBytes('\n')
			if err != nil {
				done <- readResult{err: fmt.Errorf("read: %w", err)}
				return
			}
			if len(line) == 0 || (len(line) == 1 && line[0] == '\n') {
				continue
			}
			var resp jsonRPCResponse
			if err := json.Unmarshal(line, &resp); err != nil {
				// A non-response frame (notification or unknown shape).
				// Skip and keep reading. Real MCP traffic doesn't
				// commonly hit this path.
				continue
			}
			if resp.ID != id {
				continue
			}
			done <- readResult{resp: &resp}
			return
		}
	}()

	select {
	case rr := <-done:
		if rr.err != nil {
			return nil, rr.err
		}
		if rr.resp.Error != nil {
			return nil, rr.resp.Error
		}
		return rr.resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification — no id, no response. Used
// only for `notifications/initialized`; if more are needed later
// they can call this directly.
func (r *ReverseTranscoder) notify(method string, params any) error {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	encoded, err := json.Marshal(jsonRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := r.stdin.Write(encoded); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// --- MCP → registry helpers ----------------------------------

// parseServerInfo extracts name+version from the initialize result.
// MCP's serverInfo is `{name, version}`; both are optional but
// every real-world server populates them.
func (r *ReverseTranscoder) parseServerInfo(raw json.RawMessage) {
	var parsed struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// Be lenient — server name/version are nice-to-have, not
		// required for tool dispatch. Fall back to "unknown".
		r.serverName = "unknown-mcp-server"
		r.serverVersion = "unknown"
		return
	}
	r.serverName = parsed.ServerInfo.Name
	if r.serverName == "" {
		r.serverName = "unknown-mcp-server"
	}
	r.serverVersion = parsed.ServerInfo.Version
	if r.serverVersion == "" {
		r.serverVersion = "unknown"
	}
}

// parseMCPToolList builds ToolDefinitions from an MCP tools/list
// response. Each tool gets the supplied handler bound — typically
// callTool, which routes back through tools/call.
//
// MCP shape: `{ "tools": [ {name, description, inputSchema}, ... ] }`
// where inputSchema is a JSON Schema object. We surface the same
// shape in registry.ToolDefinition.
func parseMCPToolList(raw json.RawMessage, handler func(context.Context, *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse) []*registry.ToolDefinition {
	var parsed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	out := make([]*registry.ToolDefinition, 0, len(parsed.Tools))
	for _, t := range parsed.Tools {
		schemaPB, _ := jsonRawToStruct(t.InputSchema)
		out = append(out, &registry.ToolDefinition{
			Name:               t.Name,
			SummaryDescription: firstLine(t.Description),
			LongDescription:    t.Description,
			InputSchema:        schemaPB,
			Tags:               []string{"mcp"},
			Idempotency:        "unknown",
			Handler:            handler,
		})
	}
	return out
}

// jsonRawToStruct decodes a JSON object into structpb. Returns nil
// (no error) when the input is empty or not an object — schemas
// are optional in MCP, callers shouldn't crash on missing ones.
func jsonRawToStruct(raw json.RawMessage) (*structpb.Struct, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, err
	}
	return structpb.NewStruct(asMap)
}

// firstLine returns up to the first newline. Used to derive a short
// SummaryDescription from MCP's single Description field.
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// mcpResultToCallResponse adapts an MCP tools/call result into the
// codefly Toolbox CallToolResponse shape.
//
// MCP shape: `{ content: [...], isError: bool }` where each content
// item is `{type: "text"|"image"|"resource", text?, data?, ...}`.
// We map text → Content_Text and any other type to a structured
// blob containing the raw JSON (best-effort fidelity until specific
// types matter).
func mcpResultToCallResponse(raw json.RawMessage) *toolboxv0.CallToolResponse {
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return &toolboxv0.CallToolResponse{Error: "mcp result decode: " + err.Error()}
	}
	if parsed.IsError {
		// MCP carries error text in the same content array. Collapse
		// to a single Error string for the Toolbox shape.
		var msg string
		for _, c := range parsed.Content {
			if c.Type == "text" {
				msg += c.Text
			}
		}
		if msg == "" {
			msg = "mcp tool returned isError=true with no text content"
		}
		return &toolboxv0.CallToolResponse{Error: msg}
	}
	out := make([]*toolboxv0.Content, 0, len(parsed.Content))
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out = append(out, &toolboxv0.Content{
				Body: &toolboxv0.Content_Text{Text: c.Text},
			})
		}
		// Non-text MCP content (image, resource) is dropped for now.
		// A later commit can map them to Content_Blob with proper
		// media-type handling.
	}
	return &toolboxv0.CallToolResponse{Content: out}
}
