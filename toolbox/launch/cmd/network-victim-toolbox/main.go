// Command network-victim-toolbox is a TEST-ONLY plugin used by the
// end-to-end sandbox-enforcement tests in core/toolbox/launch.
//
// It exposes five tools without ANY application-layer guard:
//
//   - net.fetch  — issues an outbound HTTP GET. Used to test that
//     a deny-network sandbox blocks the syscall.
//     (NOTE: deny-network ALSO breaks loopback gRPC,
//     so the e2e test today gates this only when the
//     plugin can still boot.)
//
//   - fs.write   — writes a file at a caller-supplied path
//     IN-PROCESS via os.WriteFile. Tests that the
//     plugin process itself is confined.
//
//   - exec.write — writes a file at a caller-supplied path by
//     spawning a /bin/sh child (`/bin/sh -c "echo X
//     > path"`). Tests that the OS sandbox is
//     inherited by child processes — the load-bearing
//     property that lets us confine plugins ONCE at
//     spawn instead of inside every plugin.
//
//   - who.am.i   — returns the Principal stamped on the call's
//     context (set by core/agents.principalUnary
//     Interceptor from the env/header). Used by the
//     permission-system E2E to verify the principal
//     traveled from the host's WithPrincipal(p)
//     through the gRPC metadata into the handler's
//     ctx.
//
//   - check.action — calls policy.AuthorizerFromContext(ctx).
//     Authorized(action, resource) and returns the
//     verdict structure. Demonstrates the inline
//     fine-grained permission check pattern for
//     plugin authors. Used by the E2E to verify
//     the host→plugin→host callback channel works
//     end-to-end.
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
	"os/exec"
	"time"

	"github.com/codefly-dev/core/agents"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/toolbox/policyguard"
	"google.golang.org/protobuf/types/known/structpb"
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
			{Name: "exec.write", Description: "Write a file by spawning /bin/sh -c. Tests sandbox inheritance into child processes."},
			{Name: "who.am.i", Description: "Return the Principal from request context. Tests permission-system identity wire."},
			{Name: "check.action", Description: "Call Authorized(action, resource) on the host's permission callback. Tests plugin→host callback channel."},
		},
	}, nil
}

func (victimServer) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "net.fetch":
		return doFetch(ctx, req)
	case "fs.write":
		return doWrite(req)
	case "exec.write":
		return doExecWrite(ctx, req)
	case "who.am.i":
		return doWhoAmI(ctx)
	case "check.action":
		return doCheckAction(ctx, req)
	default:
		return &toolboxv0.CallToolResponse{Error: "unknown tool: " + req.Name}, nil
	}
}

// doCheckAction is the reference implementation of inline
// Authorized() usage for plugin authors. It takes (action,
// resource) from the request, calls the host's permission
// callback via the AuthorizerFromContext, and returns the verdict.
//
// **Real-world pattern for plugin authors.** Inside a tool that's
// already been outer-authorized, you may want to gate sub-
// operations:
//
//	func (s *server) GetData(ctx, req) (*Resp, error) {
//	    a := policy.AuthorizerFromContext(ctx)
//	    data := getBasic()
//	    allowed, _, _ := a.Authorized(ctx, "data.read_secrets", req.ResourceID)
//	    if allowed {
//	        data.Secrets = getSecrets()
//	    }
//	    return data, nil
//	}
//
// The plugin doesn't need to know HOW the host decides — it just
// asks. The PDP / saas-starter answer.
//
// **What this tool surfaces (for tests).** Returns a structured
// response with allowed/reason/error fields so the E2E test can
// assert all three failure modes — clean policy deny, allow,
// transport error.
func doCheckAction(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	if req.Arguments == nil {
		return &toolboxv0.CallToolResponse{Error: "check.action requires action and resource arguments"}, nil
	}
	args := req.Arguments.AsMap()
	action, _ := args["action"].(string)
	resource, _ := args["resource"].(string)
	if action == "" {
		return &toolboxv0.CallToolResponse{Error: "check.action requires non-empty action"}, nil
	}

	authorizer := policy.AuthorizerFromContext(ctx)
	allowed, reason, err := authorizer.Authorized(ctx, action, resource)

	result := map[string]any{
		"allowed":  allowed,
		"reason":   reason,
		"action":   action,
		"resource": resource,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	s, _ := structpb.NewStruct(result)
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{{Body: &toolboxv0.Content_Structured{Structured: s}}},
	}, nil
}

// doWhoAmI returns the Principal stamped on ctx by the
// principalUnaryInterceptor, structured for the E2E test to
// assert against.
//
// **What this proves.** The host called manager.Load with
// WithPrincipal(p), which set CODEFLY_PRINCIPAL_TOKEN as env on
// this plugin process. The plugin's gRPC interceptor reads the
// token, decodes, validates, and stamps the Principal on each
// call's ctx. This handler reads it back — same value the host
// put in. End-to-end identity wire test.
//
// **Plugin-author note.** Reading PrincipalFrom is fine for
// AUDIT or DISPLAY purposes. NEVER branch on the Principal to
// make authorization decisions in plugin code — that's the PDP's
// job. If you find yourself writing `if p.Kind == "human"` to
// gate behavior, you're rebuilding the auth layer in the wrong
// place. The principal is read-only metadata from the plugin's
// perspective.
func doWhoAmI(ctx context.Context) (*toolboxv0.CallToolResponse, error) {
	p := policy.PrincipalFrom(ctx)
	if p == nil {
		// Empty response indicates "no principal stamped" — the
		// E2E test asserts against this distinct case.
		s, _ := structpb.NewStruct(map[string]any{"present": false})
		return &toolboxv0.CallToolResponse{
			Content: []*toolboxv0.Content{{Body: &toolboxv0.Content_Structured{Structured: s}}},
		}, nil
	}
	s, _ := structpb.NewStruct(map[string]any{
		"present":      true,
		"id":           p.ID,
		"kind":         p.Kind,
		"org_id":       p.OrgID,
		"agent_id":     p.AgentID,
		"display_name": p.DisplayName,
		"chain_len":    float64(len(p.DelegationChain)),
	})
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{{Body: &toolboxv0.Content_Structured{Structured: s}}},
	}, nil
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

// doExecWrite is the inheritance probe: the plugin spawns a child
// /bin/sh that runs `echo CONTENT > PATH`. The shell is what issues
// the open(O_WRONLY) syscall, not the plugin itself. If the OS
// sandbox is inherited by descendants (the load-bearing property),
// a forbidden path must fail just like fs.write — proving codefly's
// single-point enforcement at manager.Load reaches plugin children.
//
// Output discipline: capture combined stdout+stderr verbatim. The
// shell's "Permission denied" / "Read-only file system" message is
// part of the sandbox-blockage signal the test asserts on. We do
// NOT redact /bin/sh exit codes — the test wants to see them.
func doExecWrite(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	if req.Arguments == nil {
		return &toolboxv0.CallToolResponse{Error: "exec.write requires path argument"}, nil
	}
	args := req.Arguments.AsMap()
	path, _ := args["path"].(string)
	if path == "" {
		return &toolboxv0.CallToolResponse{Error: "exec.write requires non-empty path"}, nil
	}
	content, _ := args["content"].(string)
	if content == "" {
		content = "child-victim-write"
	}

	// Single-quote the content so the shell doesn't expand $vars,
	// backticks, etc. — keep the syscall behavior deterministic.
	// Escape any embedded single-quotes by closing/opening the
	// single-quoted string, which is the standard portable trick.
	script := fmt.Sprintf(`printf '%%s\n' %s > %s`, shellQuote(content), shellQuote(path))

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &toolboxv0.CallToolResponse{
			Error: fmt.Sprintf("exec.write failed: %v: %s", err, string(out)),
		}, nil
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Text{Text: "child wrote: " + path}},
		},
	}, nil
}

// shellQuote wraps s in single-quotes, escaping any embedded
// single-quote with the close-escape-open pattern.
func shellQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}

func main() {
	// Build the victim toolbox.
	victim := victimServer{}

	// Conditional wrapping pattern: the test fixture wraps with
	// policyguard.Guard ONLY when the host has wired enforcement
	// (scoped-auth secret OR permissions callback). Without
	// those, the legacy passthrough preserves backward compat
	// with sandbox/principal-flow E2E tests that pre-date the
	// permission system and don't expect an outer auth gate.
	//
	// In a production plugin, you'd unconditionally wrap with
	// the Guard configured to fail closed; this fixture
	// deliberately mirrors what an operator-configurable
	// plugin might offer.
	hasScopedAuth := os.Getenv("CODEFLY_SCOPED_AUTHZ_SECRET") != ""
	hasCallback := os.Getenv("CODEFLY_PERMISSIONS_SOCKET") != ""

	var toolbox toolboxv0.ToolboxServer = &victim
	if hasScopedAuth || hasCallback {
		audience := "codefly.dev/network-victim:" + os.Getenv("CODEFLY_TOOLBOX_VERSION")
		// PDP for the Guard's defense path. callback-backed
		// when the host configured permissions callback; else
		// AllowAllPDP (only the scoped-auth fast path enforces).
		var pdp policy.PDP = policy.AllowAllPDP{}
		if hasCallback {
			pdp = policy.NewCallbackPDPFromEnv()
		}
		toolbox = policyguard.New(&victim, pdp, audience).
			WithAudience(audience)
	}

	agents.Serve(agents.PluginRegistration{
		Toolbox: toolbox,
	})
}
