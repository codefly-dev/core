package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/git"
	"github.com/codefly-dev/core/toolbox/mcp"
)

// runGit invokes the git binary in dir with the given args; returns
// the stdout/stderr-combined output on failure.
func runGit(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		return &gitErr{args: args, out: string(out), err: err}
	}
	return nil
}

// runGitWithEnv is runGit but seeded with deterministic
// author/committer identity so go-git tests don't depend on the
// host's git config.
func runGitWithEnv(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := c.CombinedOutput(); err != nil {
		return &gitErr{args: args, out: string(out), err: err}
	}
	return nil
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
}

type gitErr struct {
	args []string
	out  string
	err  error
}

func (g *gitErr) Error() string {
	return "git " + strings.Join(g.args, " ") + ": " + g.err.Error() + "\n" + g.out
}

// startInProcessGitToolbox stands up the real git toolbox behind a
// bufconn so the transcoder talks to a real ToolboxClient. No
// subprocess: cheaper than spawning the plugin, sufficient for
// transcoder-only tests.
func startInProcessGitToolbox(t *testing.T, workspace string) toolboxv0.ToolboxClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	toolboxv0.RegisterToolboxServer(srv, git.New(workspace, "mcp-test"))
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return toolboxv0.NewToolboxClient(conn)
}

// runTranscoder sends `requests` (a slice of JSON-RPC frames) to a
// transcoder backed by the given client, then returns whatever the
// transcoder wrote back. Helper because plumbing pipes for stdio
// transports is verbose at every test site.
func runTranscoder(t *testing.T, client toolboxv0.ToolboxClient, requests []string) []map[string]any {
	t.Helper()

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer

	tc := mcp.New(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, tc.Serve(ctx, in, &out))

	// Each response is one JSON object on its own line.
	var responses []map[string]any
	dec := json.NewDecoder(&out)
	for {
		var frame map[string]any
		if err := dec.Decode(&frame); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode response frame: %v", err)
		}
		responses = append(responses, frame)
	}
	return responses
}

// makeRequest builds a JSON-RPC 2.0 request line — saves quoting
// pain at every test site.
func makeRequest(t *testing.T, id any, method string, params any) string {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return string(b)
}

// initGitRepo helper — duplicated across toolbox test files to keep
// them self-contained.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, runGit(dir, "init"))
	require.NoError(t, writeFile(dir, "README.md", "# test\n"))
	require.NoError(t, runGit(dir, "add", "README.md"))
	require.NoError(t, runGitWithEnv(dir, "commit", "-m", "initial"))
}

// --- Tests -------------------------------------------------------

func TestTranscoder_Initialize_ReturnsServerInfo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	client := startInProcessGitToolbox(t, dir)

	resps := runTranscoder(t, client, []string{
		makeRequest(t, 1, "initialize", map[string]any{}),
	})
	require.Len(t, resps, 1)

	require.Equal(t, "2.0", resps[0]["jsonrpc"])
	result, _ := resps[0]["result"].(map[string]any)
	require.NotNil(t, result, "initialize must produce a result, not an error: %v", resps[0])

	require.Equal(t, mcp.MCPProtocolVersion, result["protocolVersion"])
	info, _ := result["serverInfo"].(map[string]any)
	require.Equal(t, "git", info["name"], "serverInfo.name must come from the toolbox Identity")
	require.Equal(t, "mcp-test", info["version"])

	caps, _ := result["capabilities"].(map[string]any)
	require.Contains(t, caps, "tools",
		"a transcoded toolbox always declares the tools capability")
}

func TestTranscoder_ToolsList_MapsListToolsResponse(t *testing.T) {
	client := startInProcessGitToolbox(t, t.TempDir())
	resps := runTranscoder(t, client, []string{
		makeRequest(t, 1, "tools/list", map[string]any{}),
	})
	require.Len(t, resps, 1)

	result, _ := resps[0]["result"].(map[string]any)
	require.NotNil(t, result, "tools/list must succeed: %v", resps[0])
	tools, _ := result["tools"].([]any)
	require.NotEmpty(t, tools)

	// Confirm at least git.status appears with an inputSchema —
	// MCP requires inputSchema on every tool, the transcoder must
	// fill it in even for zero-arg tools.
	found := false
	for _, raw := range tools {
		t1, _ := raw.(map[string]any)
		if t1["name"] == "git.status" {
			found = true
			require.NotNil(t, t1["inputSchema"],
				"every transcoded tool must have an inputSchema (MCP requirement)")
		}
	}
	require.True(t, found, "git.status must appear in the transcoded tool list")
}

func TestTranscoder_ToolsCall_RoundTripStructuredContent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	client := startInProcessGitToolbox(t, dir)

	resps := runTranscoder(t, client, []string{
		makeRequest(t, 1, "tools/call", map[string]any{
			"name": "git.status",
		}),
	})
	require.Len(t, resps, 1)

	result, _ := resps[0]["result"].(map[string]any)
	require.NotNil(t, result, "tools/call must succeed: %v", resps[0])
	require.Equal(t, false, result["isError"], "fresh repo is clean — must not be reported as error")

	contents, _ := result["content"].([]any)
	require.Len(t, contents, 1, "git.status returns one Content block")
	first, _ := contents[0].(map[string]any)
	require.Equal(t, "text", first["type"], "structured content must be flattened to text in MCP")

	// The text is a JSON dump of the structured payload — confirm
	// it parses back into something git.status would have produced.
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(first["text"].(string)), &payload))
	require.Equal(t, true, payload["clean"], "round-tripped JSON must reflect git.status output")
}

func TestTranscoder_ToolsCall_ErrorIsSurfacedWithIsErrorTrue(t *testing.T) {
	client := startInProcessGitToolbox(t, t.TempDir())

	// Call an unknown tool — toolbox returns an error in the
	// CallToolResponse; transcoder must surface that as isError=true.
	resps := runTranscoder(t, client, []string{
		makeRequest(t, 1, "tools/call", map[string]any{"name": "git.does-not-exist"}),
	})
	require.Len(t, resps, 1)
	result, _ := resps[0]["result"].(map[string]any)
	require.NotNil(t, result)
	require.Equal(t, true, result["isError"],
		"toolbox-level error must produce MCP isError=true (not a JSON-RPC error)")

	contents, _ := result["content"].([]any)
	require.NotEmpty(t, contents, "the error message must be surfaced as a content block")
	first, _ := contents[0].(map[string]any)
	require.Contains(t, first["text"].(string), "git.does-not-exist")
}

func TestTranscoder_UnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	client := startInProcessGitToolbox(t, t.TempDir())
	resps := runTranscoder(t, client, []string{
		makeRequest(t, 1, "totally/unknown", nil),
	})
	require.Len(t, resps, 1)
	errObj, _ := resps[0]["error"].(map[string]any)
	require.NotNil(t, errObj, "unknown method must produce a JSON-RPC error envelope: %v", resps[0])
	require.EqualValues(t, -32601, errObj["code"], "method not found = -32601 per JSON-RPC 2.0 spec")
}

func TestTranscoder_Notification_ProducesNoResponse(t *testing.T) {
	client := startInProcessGitToolbox(t, t.TempDir())
	// Frame with no id field is a notification — server MUST NOT
	// respond. We use a real method name so it dispatches; an
	// unknown method as a notification would also produce no reply,
	// but using a real one tests the non-error path's notification
	// gate too.
	notification := `{"jsonrpc":"2.0","method":"tools/list"}`
	resps := runTranscoder(t, client, []string{notification})
	require.Empty(t, resps,
		"notifications (no id) must produce zero responses, per JSON-RPC 2.0 spec")
}

func TestTranscoder_MalformedJSON_RecoversAndContinues(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	client := startInProcessGitToolbox(t, dir)
	resps := runTranscoder(t, client, []string{
		`{this is not json`,
		makeRequest(t, 2, "tools/list", nil),
	})
	require.Len(t, resps, 2,
		"a malformed frame must produce a parse-error reply WITHOUT terminating the loop")

	// First reply: parse error with id=null (we never read an id).
	errObj, _ := resps[0]["error"].(map[string]any)
	require.NotNil(t, errObj)
	require.EqualValues(t, -32700, errObj["code"], "parse error = -32700")

	// Second reply: tools/list succeeded, proving the loop kept
	// going.
	require.Contains(t, resps[1], "result",
		"second request must succeed; transcoder must not bail on the first parse error")
}
