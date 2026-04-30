package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

// JSONRPCVersion is pinned in every reply. Per JSON-RPC 2.0 spec
// the field must be exactly the string "2.0".
const JSONRPCVersion = "2.0"

// MCPProtocolVersion advertises which MCP revision the transcoder
// targets. Bumping this is a coordinated change with downstream
// clients; currently aligned with the cli/pkg/mcp server.
const MCPProtocolVersion = "2024-11-05"

// JSON-RPC error codes. Same set the existing cli MCP server uses;
// kept local so the transcoder has no cli dependency. Only the
// codes the transcoder actually emits are kept — InvalidRequest is
// pruned because we surface malformed frames as ParseError.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Transcoder wraps a toolboxv0.ToolboxClient and serves the MCP
// JSON-RPC protocol against it. One Transcoder owns one client;
// concurrent Serve calls against the same Transcoder are NOT safe
// (writes would interleave on the shared Writer).
type Transcoder struct {
	client toolboxv0.ToolboxClient

	// writeMu serializes JSON-RPC writes. Multiple in-flight requests
	// from the client could otherwise race on Writer.Write and
	// produce a corrupted stream.
	writeMu sync.Mutex
}

// New returns a Transcoder bound to the given toolbox client. The
// client must already be connected (e.g. host.Plugin.Client()).
// Closing the underlying connection is the caller's responsibility;
// Transcoder doesn't own the lifecycle.
func New(client toolboxv0.ToolboxClient) *Transcoder {
	return &Transcoder{client: client}
}

// Serve reads JSON-RPC frames from r, dispatches each to the toolbox
// over gRPC, and writes the result back to w. Returns when r yields
// io.EOF or ctx is cancelled. A malformed request is replied with a
// ParseError but does NOT terminate the loop — MCP clients recover
// from individual bad frames.
//
// Stdio transport: pass os.Stdin and os.Stdout. JSON-RPC framing is
// "one JSON object per line"; bufio.Scanner with the default split
// fits.
func (t *Transcoder) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	// Default scanner buffer is 64KB; large tool responses can blow
	// past that. Lift the cap to 4MiB to mirror MaxEvalOutputBytes /
	// MaxBodyBytes — anything bigger is almost certainly a tool bug.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			t.writeError(w, nil, codeParseError, "invalid JSON: "+err.Error())
			continue
		}
		t.dispatch(ctx, w, &req)

		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

// dispatch routes a single request to its handler and writes the
// response. Notifications (no id) get no response per JSON-RPC 2.0
// spec.
func (t *Transcoder) dispatch(ctx context.Context, w io.Writer, req *jsonRPCRequest) {
	notification := req.ID == nil

	// Method routing. Each case parses params (if needed), calls the
	// toolbox over gRPC, and writes the typed result. Unknown methods
	// surface MethodNotFound — the standard JSON-RPC reply for "I
	// don't know that one."
	switch req.Method {
	case "initialize":
		result, err := t.handleInitialize(ctx)
		t.replyOrError(w, req.ID, notification, result, err, codeInternalError)
	case "tools/list":
		result, err := t.handleListTools(ctx)
		t.replyOrError(w, req.ID, notification, result, err, codeInternalError)
	case "tools/call":
		result, err := t.handleCallTool(ctx, req.Params)
		t.replyOrError(w, req.ID, notification, result, err, codeInvalidParams)
	case "resources/list":
		result, err := t.handleListResources(ctx)
		t.replyOrError(w, req.ID, notification, result, err, codeInternalError)
	case "resources/read":
		result, err := t.handleReadResource(ctx, req.Params)
		t.replyOrError(w, req.ID, notification, result, err, codeInvalidParams)
	case "prompts/list":
		result, err := t.handleListPrompts(ctx)
		t.replyOrError(w, req.ID, notification, result, err, codeInternalError)
	case "prompts/get":
		result, err := t.handleGetPrompt(ctx, req.Params)
		t.replyOrError(w, req.ID, notification, result, err, codeInvalidParams)
	case "ping":
		// Standard MCP keep-alive. Reply with empty object — clients
		// just want to confirm the channel is alive.
		t.replyOrError(w, req.ID, notification, struct{}{}, nil, codeInternalError)
	default:
		if !notification {
			t.writeError(w, req.ID, codeMethodNotFound, "unknown method: "+req.Method)
		}
	}
}

// replyOrError writes a typed result on success or a JSON-RPC error
// on failure. Skips writing for notifications (id absent).
func (t *Transcoder) replyOrError(
	w io.Writer, id any, notification bool, result any, err error, errCode int,
) {
	if notification {
		return
	}
	if err != nil {
		t.writeError(w, id, errCode, err.Error())
		return
	}
	t.writeResult(w, id, result)
}

// --- Handlers ----------------------------------------------------

func (t *Transcoder) handleInitialize(ctx context.Context) (any, error) {
	id, err := t.client.Identity(ctx, &toolboxv0.IdentityRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox Identity: %w", err)
	}
	return map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    id.GetName(),
			"version": id.GetVersion(),
		},
		// Surface the toolbox's CanonicalFor binaries and sandbox
		// summary as MCP-side metadata. Non-standard MCP fields, but
		// MCP allows arbitrary extensions in serverInfo's surrounding
		// object — clients ignore unknowns.
		"codefly": map[string]any{
			"description":     id.GetDescription(),
			"canonical_for":   id.GetCanonicalFor(),
			"sandbox_summary": id.GetSandboxSummary(),
		},
	}, nil
}

func (t *Transcoder) handleListTools(ctx context.Context) (any, error) {
	resp, err := t.client.ListTools(ctx, &toolboxv0.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox ListTools: %w", err)
	}
	tools := make([]map[string]any, 0, len(resp.GetTools()))
	for _, tl := range resp.GetTools() {
		entry := map[string]any{
			"name":        tl.GetName(),
			"description": tl.GetDescription(),
		}
		if tl.GetInputSchema() != nil {
			entry["inputSchema"] = tl.GetInputSchema().AsMap()
		} else {
			// MCP requires inputSchema. Empty-object schema is the
			// "no arguments" sentinel.
			entry["inputSchema"] = map[string]any{"type": "object"}
		}
		tools = append(tools, entry)
	}
	return map[string]any{"tools": tools}, nil
}

func (t *Transcoder) handleCallTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("invalid tools/call params: %w", err)
		}
	}
	if params.Name == "" {
		return nil, fmt.Errorf("tools/call: name is required")
	}

	var args *structpb.Struct
	if len(params.Arguments) > 0 {
		s, err := structpb.NewStruct(params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tools/call: arguments not JSON-shaped: %w", err)
		}
		args = s
	}

	resp, err := t.client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name:      params.Name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("toolbox CallTool: %w", err)
	}

	// MCP CallToolResult → array of Content blocks + isError flag.
	// We translate every toolbox Content variant: text → text,
	// structured → text (JSON-stringified), blob → resource (with
	// data URI; large blobs are out of scope until v2).
	out := map[string]any{
		"isError": resp.GetError() != "" || resp.GetCanonicalRouted(),
	}
	contents := make([]map[string]any, 0, len(resp.GetContent())+1)
	if resp.GetError() != "" {
		// Surface the error as a text content block — MCP doesn't
		// distinguish error-text from regular text in the response
		// envelope, only via isError.
		contents = append(contents, map[string]any{
			"type": "text",
			"text": "error: " + resp.GetError(),
		})
	}
	for _, c := range resp.GetContent() {
		if txt := c.GetText(); txt != "" {
			contents = append(contents, map[string]any{"type": "text", "text": txt})
			continue
		}
		if s := c.GetStructured(); s != nil {
			// MCP doesn't have a "structured" content type; flatten to
			// a JSON-stringified text block so clients see the data
			// even if they can't reason about it as typed fields.
			b, jerr := json.Marshal(s.AsMap())
			if jerr != nil {
				return nil, fmt.Errorf("toolbox CallTool: cannot marshal structured content: %w", jerr)
			}
			contents = append(contents, map[string]any{"type": "text", "text": string(b)})
			continue
		}
		if blob := c.GetBlob(); blob != nil {
			// Best-effort: surface the media type and a length hint
			// rather than the raw bytes. Inline binary in MCP
			// responses works but is uncommon and risky for size.
			contents = append(contents, map[string]any{
				"type": "text",
				"text": fmt.Sprintf("[blob %s, %d bytes]", blob.GetMediaType(), len(blob.GetData())),
			})
		}
	}
	out["content"] = contents
	return out, nil
}

func (t *Transcoder) handleListResources(ctx context.Context) (any, error) {
	resp, err := t.client.ListResources(ctx, &toolboxv0.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox ListResources: %w", err)
	}
	resources := make([]map[string]any, 0, len(resp.GetResources()))
	for _, r := range resp.GetResources() {
		resources = append(resources, map[string]any{
			"uri":         r.GetUri(),
			"name":        r.GetName(),
			"description": r.GetDescription(),
			"mimeType":    r.GetMimeType(),
		})
	}
	return map[string]any{"resources": resources}, nil
}

func (t *Transcoder) handleReadResource(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("invalid resources/read params: %w", err)
		}
	}
	if params.URI == "" {
		return nil, fmt.Errorf("resources/read: uri is required")
	}

	resp, err := t.client.ReadResource(ctx, &toolboxv0.ReadResourceRequest{Uri: params.URI})
	if err != nil {
		return nil, fmt.Errorf("toolbox ReadResource: %w", err)
	}

	contents := make([]map[string]any, 0, len(resp.GetContent()))
	for _, c := range resp.GetContent() {
		entry := map[string]any{"uri": params.URI}
		if txt := c.GetText(); txt != "" {
			entry["text"] = txt
			contents = append(contents, entry)
			continue
		}
		if s := c.GetStructured(); s != nil {
			b, jerr := json.Marshal(s.AsMap())
			if jerr != nil {
				return nil, fmt.Errorf("toolbox ReadResource: marshal structured: %w", jerr)
			}
			entry["text"] = string(b)
			entry["mimeType"] = "application/json"
			contents = append(contents, entry)
			continue
		}
		if blob := c.GetBlob(); blob != nil {
			entry["mimeType"] = blob.GetMediaType()
			// MCP encodes binary as base64 in the "blob" field. We
			// pass through the raw bytes; encoding/json marshals
			// []byte as base64 by default.
			entry["blob"] = blob.GetData()
			contents = append(contents, entry)
		}
	}
	return map[string]any{"contents": contents}, nil
}

func (t *Transcoder) handleListPrompts(ctx context.Context) (any, error) {
	resp, err := t.client.ListPrompts(ctx, &toolboxv0.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox ListPrompts: %w", err)
	}
	prompts := make([]map[string]any, 0, len(resp.GetPrompts()))
	for _, p := range resp.GetPrompts() {
		args := make([]map[string]any, 0, len(p.GetArguments()))
		for _, a := range p.GetArguments() {
			args = append(args, map[string]any{
				"name":        a.GetName(),
				"description": a.GetDescription(),
				"required":    a.GetRequired(),
			})
		}
		prompts = append(prompts, map[string]any{
			"name":        p.GetName(),
			"description": p.GetDescription(),
			"arguments":   args,
		})
	}
	return map[string]any{"prompts": prompts}, nil
}

func (t *Transcoder) handleGetPrompt(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("invalid prompts/get params: %w", err)
		}
	}
	if params.Name == "" {
		return nil, fmt.Errorf("prompts/get: name is required")
	}
	var args *structpb.Struct
	if len(params.Arguments) > 0 {
		s, err := structpb.NewStruct(params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("prompts/get: arguments not JSON-shaped: %w", err)
		}
		args = s
	}
	resp, err := t.client.GetPrompt(ctx, &toolboxv0.GetPromptRequest{
		Name:      params.Name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("toolbox GetPrompt: %w", err)
	}
	messages := make([]map[string]any, 0, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		content := make([]map[string]any, 0, len(m.GetContent()))
		for _, c := range m.GetContent() {
			if txt := c.GetText(); txt != "" {
				content = append(content, map[string]any{"type": "text", "text": txt})
			}
		}
		messages = append(messages, map[string]any{
			"role":    m.GetRole(),
			"content": content,
		})
	}
	return map[string]any{
		"description": resp.GetDescription(),
		"messages":    messages,
	}, nil
}

// --- JSON-RPC framing --------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *jsonRPCErrObj `json:"error,omitempty"`
}

type jsonRPCErrObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// writeResult emits a successful response. One JSON object per line —
// the line framing the cli/pkg/mcp server uses too.
func (t *Transcoder) writeResult(w io.Writer, id, result any) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	resp := jsonRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  result,
	}
	t.writeFrame(w, resp)
}

func (t *Transcoder) writeError(w io.Writer, id any, code int, message string) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	resp := jsonRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &jsonRPCErrObj{Code: code, Message: message},
	}
	t.writeFrame(w, resp)
}

func (t *Transcoder) writeFrame(w io.Writer, resp jsonRPCResponse) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // structured JSON shouldn't get HTML escapes
	if err := enc.Encode(resp); err != nil {
		// We can't surface this through the channel we just failed
		// to write to. Best we can do is best-effort log to stderr;
		// callers who care should pass a logged Writer.
		fmt.Fprintf(io.Discard, "transcoder write: %v\n", err)
	}
}
