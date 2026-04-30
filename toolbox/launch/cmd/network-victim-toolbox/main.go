// Command network-victim-toolbox is a TEST-ONLY plugin used by the
// end-to-end sandbox-enforcement tests in core/toolbox/launch.
//
// It exposes two tools without ANY application-layer guard:
//
//   - net.fetch — issues an outbound HTTP GET. Used to test that
//                 a deny-network sandbox blocks the syscall.
//                 (NOTE: deny-network ALSO breaks loopback gRPC,
//                 so the e2e test today gates this only when the
//                 plugin can still boot.)
//
//   - fs.write  — writes a file at a caller-supplied path. Used to
//                 test that a sandbox declaring write_paths=[X]
//                 actually blocks writes to paths outside X.
//
// We use this test plugin instead of the real web/git toolboxes
// because they have application-layer guards (web's allowlist,
// git's repo path) that would refuse the call BEFORE the syscall,
// masking the OS layer signal.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/codefly-dev/core/agents"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

type victimServer struct {
	toolboxv0.UnimplementedToolboxServer
}

func (victimServer) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "network-victim",
		Version:        os.Getenv("CODEFLY_TOOLBOX_VERSION"),
		Description:    "Test-only victim plugin: outbound HTTP and FS writes with NO application guards. Sandbox is the only gate.",
		SandboxSummary: "no application-layer enforcement; relies entirely on OS sandbox",
	}, nil
}

func (victimServer) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{
		Tools: []*toolboxv0.Tool{
			{Name: "net.fetch", Description: "Issue HTTP GET with no allowlist; sandbox is the only gate."},
			{Name: "fs.write", Description: "Write a file at the supplied path; sandbox is the only gate."},
		},
	}, nil
}

func (victimServer) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "net.fetch":
		return doFetch(ctx, req)
	case "fs.write":
		return doWrite(req)
	default:
		return &toolboxv0.CallToolResponse{Error: "unknown tool: " + req.Name}, nil
	}
}

func doFetch(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	url := "http://example.com"
	if req.Arguments != nil {
		if v, ok := req.Arguments.AsMap()["url"].(string); ok && v != "" {
			url = v
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &toolboxv0.CallToolResponse{Error: fmt.Sprintf("build request: %v", err)}, nil
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return &toolboxv0.CallToolResponse{Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Text{Text: fmt.Sprintf("status=%d body=%q", resp.StatusCode, string(body))}},
		},
	}, nil
}

func doWrite(req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	if req.Arguments == nil {
		return &toolboxv0.CallToolResponse{Error: "fs.write requires path argument"}, nil
	}
	args := req.Arguments.AsMap()
	path, _ := args["path"].(string)
	if path == "" {
		return &toolboxv0.CallToolResponse{Error: "fs.write requires non-empty path"}, nil
	}
	content, _ := args["content"].(string)
	if content == "" {
		content = "victim-write\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		// This is the success-path of the sandbox test: OS denied
		// the write. Surface verbatim.
		return &toolboxv0.CallToolResponse{Error: fmt.Sprintf("write failed: %v", err)}, nil
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Text{Text: "wrote: " + path}},
		},
	}, nil
}

func main() {
	agents.Serve(agents.PluginRegistration{
		Toolbox: victimServer{},
	})
}
