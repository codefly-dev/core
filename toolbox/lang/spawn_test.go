package lang_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/lang"
	"github.com/codefly-dev/core/toolbox/launch"
)

// installFakeLangAtAgentPath compiles the fake-lang-toolbox binary
// and places it at the canonical agent path under codeflyHome, the
// same way `codefly agent build` would in production.
func installFakeLangAtAgentPath(t *testing.T, ctx context.Context, codeflyHome string, ag *resources.Agent) {
	t.Helper()
	t.Setenv(resources.CodeflyHomeEnv, codeflyHome)

	target, err := ag.Path(ctx)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))

	cmd := exec.Command("go", "build", "-o", target,
		"github.com/codefly-dev/core/toolbox/lang/cmd/fake-lang-toolbox")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build fake-lang-toolbox failed:\n%s", out)
}

// TestSpawn_LangBridge_RoundTrip is the load-bearing Phase-B test:
// a real plugin process speaking the unified Toolbox contract is
// consumed by the typed Tooling wrapper. If this passes, every
// language plugin can drop NewToolboxFromTooling into its
// PluginRegistration and Mind can opt into the unified consumer
// surface via ToolingFromToolbox without any other changes.
//
// Wire under test:
//
//	go test process
//	  ↓ go build → install at agent path
//	manager.Load(ctx, agent)
//	  ↓ spawn → handshake → grpc dial → health probe
//	launch.Launch returns Plugin{Client: ToolboxClient}
//	  ↓
//	lang.ToolingFromToolbox(client)  ← the typed Mind wrapper
//	  ↓ typed call .ListSymbols(ctx, req)
//	  ↓   bridges to CallTool("lang.list_symbols", structpb)
//	  ↓     bridges to typed ListSymbols handler in fakeTooling
//	  ↓   returns structured Content
//	  ↓ decodes Content → typed ListSymbolsResponse
//	test asserts on the typed response
func TestSpawn_LangBridge_RoundTrip(t *testing.T) {
	codeflyHome := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "fake-lang",
		Version: "spawn-test",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "fake-lang",
			Publisher: "codefly.dev",
			Version:   "spawn-test",
		},
	}
	require.NoError(t, tb.Validate())
	installFakeLangAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.Launch(ctx, tb)
	require.NoError(t, err, "spawn must succeed against the fake-lang plugin")
	defer plugin.Close()

	// --- Direct Toolbox call ---
	id, err := plugin.Client.Identity(ctx, &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "fake-lang", id.Name)
	require.Equal(t, "spawn-test", id.Version,
		"plugin must surface the manifest's version via launch's standard env")

	// --- Through the typed Mind wrapper ---
	typed := lang.ToolingFromToolbox(plugin.Client)

	resp, err := typed.ListSymbols(ctx, &toolingv0.ListSymbolsRequest{File: "main.go"})
	require.NoError(t, err, "typed ListSymbols via spawned plugin must succeed")
	require.Len(t, resp.Symbols, 2,
		"the fake plugin's deterministic output must round-trip exactly through the bridge")
	require.Equal(t, "alpha", resp.Symbols[0].Name)
	require.Equal(t, "main.go:alpha", resp.Symbols[0].QualifiedName,
		"request fields must travel through CallTool's Struct intact")

	// Test counters — int32 values must not lose precision through
	// the structpb (float64 internally) round-trip. protojson handles
	// the cast.
	testResp, err := typed.Test(ctx, &toolingv0.TestRequest{})
	require.NoError(t, err)
	require.True(t, testResp.Success)
	require.EqualValues(t, 3, testResp.TestsRun)
	require.EqualValues(t, 3, testResp.TestsPassed)
	require.Equal(t, "fake: 3/3 ok", testResp.Output)
}
