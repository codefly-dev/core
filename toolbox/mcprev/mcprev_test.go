package mcprev_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/mcprev"
)

// fakeMCP simulates an MCP server: reads JSON-RPC requests from
// `clientWrites`, processes them, writes JSON-RPC responses to
// `serverWrites`. The transcoder, on the other end, reads from
// serverWrites and writes to clientWrites — symmetric pipe pair.
//
// This avoids needing a real MCP binary in tests; the contract is
// the JSON-RPC wire format, which we can fake faithfully.
type fakeMCP struct {
	clientReader io.Reader // stdin of fake MCP (writes from transcoder land here)
	clientWriter io.Writer // stdout of fake MCP (transcoder reads its replies here)
	tools        []map[string]any
	calls        []map[string]any // captured tools/call invocations
	stop         chan struct{}
}

func newFakeMCP(tools []map[string]any) (*fakeMCP, io.WriteCloser, io.ReadCloser) {
	// Pipe pair 1: transcoder writes → fake MCP reads (its "stdin").
	pluginStdinR, pluginStdinW := io.Pipe()
	// Pipe pair 2: fake MCP writes → transcoder reads (its "stdout").
	pluginStdoutR, pluginStdoutW := io.Pipe()

	mcp := &fakeMCP{
		clientReader: pluginStdinR,
		clientWriter: pluginStdoutW,
		tools:        tools,
		stop:         make(chan struct{}),
	}
	go mcp.run()
	return mcp, pluginStdinW, pluginStdoutR
}

func (f *fakeMCP) run() {
	dec := json.NewDecoder(f.clientReader)
	for {
		select {
		case <-f.stop:
			return
		default:
		}
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		f.handle(req)
	}
}

func (f *fakeMCP) handle(req map[string]any) {
	method, _ := req["method"].(string)
	id := req["id"]

	if id == nil {
		// Notification — ack without writing.
		return
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
	}
	switch method {
	case "initialize":
		resp["result"] = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"serverInfo": map[string]any{
				"name":    "fake-mcp",
				"version": "1.2.3",
			},
		}
	case "tools/list":
		resp["result"] = map[string]any{"tools": f.tools}
	case "tools/call":
		params, _ := req["params"].(map[string]any)
		f.calls = append(f.calls, params)
		name, _ := params["name"].(string)
		switch name {
		case "echo":
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			resp["result"] = map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": text},
				},
			}
		case "fail":
			resp["result"] = map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "tool said no"},
				},
				"isError": true,
			}
		default:
			resp["error"] = map[string]any{
				"code":    -32601,
				"message": "unknown tool: " + name,
			}
		}
	default:
		resp["error"] = map[string]any{
			"code":    -32601,
			"message": "unknown method: " + method,
		}
	}
	enc, _ := json.Marshal(resp)
	enc = append(enc, '\n')
	_, _ = f.clientWriter.Write(enc)
}

func (f *fakeMCP) close() { close(f.stop) }

// --- tests ---------------------------------------------------

func TestReverseTranscoder_Initialize_ReadsServerInfo(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input back", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	id, err := rt.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "fake-mcp", id.Name)
	require.Equal(t, "1.2.3", id.Version)
}

func TestReverseTranscoder_ListToolSummaries_ProjectsMCPTools(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input back", "inputSchema": map[string]any{"type": "object"}},
		{"name": "math.add", "description": "Add two numbers.\nReturns int.", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	resp, err := rt.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Tools, 2)

	names := make(map[string]string)
	for _, tool := range resp.Tools {
		names[tool.Name] = tool.Description
	}
	require.Contains(t, names, "echo")
	require.Contains(t, names, "math.add")
	require.Equal(t, "Add two numbers.", names["math.add"],
		"summary description should be the FIRST LINE of MCP's description")
}

func TestReverseTranscoder_DescribeTool_ReturnsLongDescription(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "math.add", "description": "Add two numbers.\nReturns int.", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	resp, err := rt.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: "math.add"})
	require.NoError(t, err)
	require.NotNil(t, resp.Tool)
	require.Equal(t, "Add two numbers.\nReturns int.", resp.Tool.Description,
		"DescribeTool returns the FULL MCP description, not just the first line")
}

func TestReverseTranscoder_CallTool_RoutesThroughMCP(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	args, err := structpb.NewStruct(map[string]any{"text": "hello world"})
	require.NoError(t, err)
	resp, err := rt.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "echo",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.NotEmpty(t, resp.Content)
	require.Contains(t, resp.Content[0].GetText(), "hello world",
		"text content survives sanitization unchanged for plain ASCII")
	require.Contains(t, resp.Content[0].GetText(), "<mcp-server-content>",
		"sanitizer wraps foreign content with attribution delimiters")

	// Verify the fake MCP saw the typed call.
	require.Len(t, mcp.calls, 1)
	require.Equal(t, "echo", mcp.calls[0]["name"])
}

func TestReverseTranscoder_CallTool_MapsIsErrorToErrorField(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "fail", "description": "Always fails", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	resp, err := rt.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "fail"})
	require.NoError(t, err)
	require.True(t, strings.Contains(resp.Error, "tool said no"),
		"MCP isError=true should surface as Toolbox.Error, not in Content")
	require.Empty(t, resp.Content)
}

// TestReverseTranscoder_Sanitize_StripsControlChars verifies the
// transcoder kills C0/C1 control characters before forwarding to
// the LLM. Real-world prompt-injection payloads use zero-width
// characters, RTL overrides, and ANSI escapes — all in those
// ranges. \n / \t / \r are kept (legitimate text formatting).
func TestReverseTranscoder_Sanitize_StripsControlChars(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	// Mix legitimate whitespace with attack-class control chars:
	//   ​ = zero-width space (invisibility)
	//   ‮ = right-to-left override (text spoofing)
	//    = ESC (ANSI escape sequences)
	//    = bell (terminal noise)
	args, _ := structpb.NewStruct(map[string]any{
		"text": "ok\nfine\t​hidden‮spoofed[31mansi",
	})
	resp, err := rt.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "echo",
		Arguments: args,
	})
	require.NoError(t, err)
	got := resp.Content[0].GetText()

	require.Contains(t, got, "ok\nfine\t",
		"newline + tab MUST survive — they're legitimate text formatting")
	require.NotContains(t, got, "​", "zero-width space must be stripped")
	require.NotContains(t, got, "‮", "RTL override must be stripped")
	require.NotContains(t, got, "", "ANSI ESC must be stripped")
	require.NotContains(t, got, "", "bell must be stripped")
}

// TestReverseTranscoder_Sanitize_CapsLength verifies that an MCP
// server can't fill the LLM's context budget. 64 KiB is the cap;
// anything larger gets truncated with a marker.
func TestReverseTranscoder_Sanitize_CapsLength(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	huge := strings.Repeat("a", 200*1024) // 200 KiB > 64 KiB cap
	args, _ := structpb.NewStruct(map[string]any{"text": huge})
	resp, err := rt.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "echo",
		Arguments: args,
	})
	require.NoError(t, err)
	got := resp.Content[0].GetText()

	require.Less(t, len(got), len(huge),
		"output must be smaller than input — cap kicked in")
	require.Contains(t, got, "[truncated by mcprev:",
		"truncation must be visible to the model, not silent")
}

// TestReverseTranscoder_Sanitize_WrapsForeignContent confirms the
// attribution delimiters are present. The model uses these to
// distinguish foreign MCP content from host-trusted instructions —
// blunts prompt-injection that tries to "break out" of the tool
// boundary.
func TestReverseTranscoder_Sanitize_WrapsForeignContent(t *testing.T) {
	mcp, stdin, stdout := newFakeMCP([]map[string]any{
		{"name": "echo", "description": "Echo input", "inputSchema": map[string]any{"type": "object"}},
	})
	defer mcp.close()

	rt := mcprev.New(stdin, stdout, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	args, _ := structpb.NewStruct(map[string]any{"text": "anything"})
	resp, err := rt.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "echo",
		Arguments: args,
	})
	require.NoError(t, err)
	got := resp.Content[0].GetText()

	require.True(t, strings.HasPrefix(got, "<mcp-server-content>"),
		"foreign content must be wrapped with an opening delimiter")
	require.True(t, strings.HasSuffix(got, "</mcp-server-content>"),
		"foreign content must be wrapped with a closing delimiter")
}

func TestReverseTranscoder_Identity_FallbackWhenServerInfoMissing(t *testing.T) {
	// MCP servers SHOULD include serverInfo but the field is optional
	// per spec. The transcoder must not crash and must surface a
	// stable fallback identity.
	pluginStdinR, _ := io.Pipe()
	_, pluginStdoutW := io.Pipe()
	defer pluginStdinR.Close()
	defer pluginStdoutW.Close()

	// Synthetic init response missing serverInfo.
	r, w := io.Pipe()
	go func() {
		raw := []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}` + "\n")
		_, _ = w.Write(raw)
		// also reply to tools/list with empty.
		raw2 := []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}` + "\n")
		_, _ = w.Write(raw2)
	}()
	rt := mcprev.New(io.Discard, r, nil)
	require.NoError(t, rt.Initialize(context.Background()))

	id, err := rt.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "unknown-mcp-server", id.Name)
	require.Equal(t, "unknown", id.Version)
}
