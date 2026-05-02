//go:build sandbox_e2e && darwin

// Loopback policy E2E — darwin-only because bwrap (Linux) doesn't
// support NetworkLoopback yet (lo is DOWN in the unshared netns,
// bringing it up needs a netns helper not yet implemented). On
// macOS it works via sandbox-exec's localhost rule. Splitting this
// into a darwin-tagged file replaces a runtime t.Fatal on Linux —
// per the no-t.Skip rule, missing-platform-support is expressed as
// a build tag, not a runtime skip-or-fail.
//
// Linux follow-up: the netns-loopback helper. See
// project_security_e2e.md.
package launch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/launch"
	"github.com/codefly-dev/core/toolbox/policyguard/policy"
)

// TestE2E_OSSandbox_BlocksOutboundNetwork_LoopbackAllowsHandshake
// is the network counterpart of the FS write test. Under the
// default Loopback policy the plugin's handshake to the agent
// loader (loopback gRPC) MUST succeed, and the plugin's outbound
// HTTP fetch to a non-localhost target MUST fail at the OS layer.
//
// This is the second load-bearing security composition test:
// proves the OS sandbox keeps loopback open for the codefly
// machinery while denying outbound for the plugin.
func TestE2E_OSSandbox_BlocksOutboundNetwork_LoopbackAllowsHandshake(t *testing.T) {
	requireEnforcingBackend(t)

	codeflyHome := resolveSymlinks(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-loopback",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-loopback",
		},
		// Non-empty CanonicalFor → isEmptyPolicy=false → sandbox
		// applied. Network unset → launch.buildSandbox defaults to
		// Loopback (the new secure default).
		CanonicalFor: []string{"network-victim"},
		Sandbox: policy.SandboxPolicy{
			ReadPaths: []string{
				"${HOME}",
				"${TMPDIR}",
				codeflyHome,
			},
			// Network unset — let launch's default (Loopback) apply.
			// This is the central assertion: the DEFAULT keeps
			// loopback open and blocks outbound.
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{Workspace: ""})
	require.NoError(t, err,
		"plugin must spawn under Loopback (handshake on 127.0.0.1 must succeed); if this fails, NetworkLoopback didn't allow loopback")
	defer plugin.Close()

	// Loopback gRPC must work — the handshake itself is loopback.
	id, err := plugin.Client.Identity(ctx, &toolboxv0.IdentityRequest{})
	require.NoError(t, err, "Identity over loopback gRPC must work under NetworkLoopback")
	require.Equal(t, "network-victim", id.Name)

	// Now attempt outbound fetch. The plugin's net.fetch tool
	// issues an HTTP GET to http://example.com — outside loopback,
	// so the OS sandbox must block.
	args, _ := structpb.NewStruct(map[string]any{"url": "http://example.com"})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "net.fetch", Arguments: args,
	})
	require.NoError(t, err, "gRPC CallTool must succeed (loopback); the FETCH inside the plugin is what fails")
	require.NotEmpty(t, resp.Error,
		"OS sandbox MUST block the outbound fetch under NetworkLoopback — got success: %s", contentSummary(resp))
	require.Contains(t, resp.Error, "fetch failed",
		"failure must surface from the plugin's error wrap")

	combined := strings.ToLower(resp.Error)
	hasNetworkSignal := strings.Contains(combined, "network") ||
		strings.Contains(combined, "connect") ||
		strings.Contains(combined, "permission") ||
		strings.Contains(combined, "operation not permitted") ||
		strings.Contains(combined, "no such host") ||
		strings.Contains(combined, "unreachable") ||
		strings.Contains(combined, "denied") ||
		strings.Contains(combined, "refused") ||
		strings.Contains(combined, "timeout")
	require.True(t, hasNetworkSignal,
		"error must indicate a network-layer failure (got: %q)", resp.Error)

	// Counterpart proof: localhost fetch MUST succeed. Without
	// this assertion, the test would still pass if the sandbox
	// blocked ALL network — including loopback. Spinning up a
	// local httptest server and confirming the plugin can reach
	// IT proves loopback is genuinely allowed, not silently denied.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello loopback"))
	}))
	defer ts.Close()

	args2, _ := structpb.NewStruct(map[string]any{"url": ts.URL})
	resp2, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "net.fetch", Arguments: args2,
	})
	require.NoError(t, err)
	require.Empty(t, resp2.Error,
		"loopback fetch MUST succeed under NetworkLoopback — got error: %s", resp2.Error)
	require.NotEmpty(t, resp2.Content, "loopback fetch must return content")
}
