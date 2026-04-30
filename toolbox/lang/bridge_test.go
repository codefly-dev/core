package lang_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"github.com/codefly-dev/core/toolbox/lang"
)

// fakeTooling is a stand-in language Tooling impl with deterministic
// outputs. Each typed RPC returns a known fixture so the round-trip
// test can assert byte-for-byte equality.
type fakeTooling struct {
	toolingv0.UnimplementedToolingServer
}

func (fakeTooling) ListSymbols(_ context.Context, req *toolingv0.ListSymbolsRequest) (*toolingv0.ListSymbolsResponse, error) {
	return &toolingv0.ListSymbolsResponse{
		Symbols: []*toolingv0.Symbol{
			{Name: "main", Signature: "func main()", QualifiedName: req.File + ":main"},
			{Name: "helper", Signature: "func helper(x int) int", QualifiedName: req.File + ":helper"},
		},
	}, nil
}

func (fakeTooling) Test(_ context.Context, _ *toolingv0.TestRequest) (*toolingv0.TestResponse, error) {
	return &toolingv0.TestResponse{
		Success:     true,
		Output:      "ok",
		TestsRun:    7,
		TestsPassed: 7,
	}, nil
}

func (fakeTooling) FindReferences(_ context.Context, req *toolingv0.FindReferencesRequest) (*toolingv0.FindReferencesResponse, error) {
	return &toolingv0.FindReferencesResponse{
		Locations: []*toolingv0.Location{
			{File: req.File, Line: req.Line, Column: req.Column},
			{File: req.File + "_test", Line: req.Line + 10, Column: 1},
		},
	}, nil
}

// startBridgedTooling stands up a bufconn-backed gRPC server with
// the fakeTooling wrapped in ToolboxFromTooling, returns a typed
// ToolingClient that goes through the unified Toolbox contract via
// ToolingFromToolbox. If this round-trips correctly, Mind sees no
// difference between calling the real Tooling client and calling
// through the bridged Toolbox.
func startBridgedTooling(t *testing.T, fake toolingv0.ToolingServer) (toolingv0.ToolingClient, toolboxv0.ToolboxClient) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	toolboxv0.RegisterToolboxServer(srv,
		lang.NewToolboxFromTooling("fake", "0.0.1", fake))
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

	tbClient := toolboxv0.NewToolboxClient(conn)
	typed := lang.ToolingFromToolbox(tbClient)
	return typed, tbClient
}

// --- Tests -------------------------------------------------------

func TestBridge_Identity_FromToolbox(t *testing.T) {
	_, tb := startBridgedTooling(t, fakeTooling{})
	id, err := tb.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "fake", id.Name)
	require.Equal(t, "0.0.1", id.Version)
}

func TestBridge_ListTools_ContainsAllConventionalNames(t *testing.T) {
	_, tb := startBridgedTooling(t, fakeTooling{})
	resp, err := tb.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	got := make(map[string]bool, len(resp.Tools))
	for _, tl := range resp.Tools {
		got[tl.Name] = true
	}
	for _, expected := range lang.AllTools {
		require.True(t, got[expected],
			"convention tool %q must appear in ListTools (got: %v)", expected, got)
	}
	require.Len(t, resp.Tools, len(lang.AllTools),
		"ListTools must return EXACTLY the convention set — no more, no less")
}

func TestBridge_RoundTrip_ListSymbols(t *testing.T) {
	typed, _ := startBridgedTooling(t, fakeTooling{})

	// Call through the typed wrapper (Mind's perspective).
	resp, err := typed.ListSymbols(context.Background(),
		&toolingv0.ListSymbolsRequest{File: "main.go"})
	require.NoError(t, err, "typed ListSymbols via bridge must succeed")

	require.Len(t, resp.Symbols, 2,
		"bridge must round-trip the typed response from the fake")
	require.Equal(t, "main", resp.Symbols[0].Name)
	require.Equal(t, "func main()", resp.Symbols[0].Signature)
	require.Equal(t, "main.go:main", resp.Symbols[0].QualifiedName,
		"the request field passed through the bridge must surface in the response")
	require.Equal(t, "helper", resp.Symbols[1].Name)
}

func TestBridge_RoundTrip_Test_TypedCounters(t *testing.T) {
	typed, _ := startBridgedTooling(t, fakeTooling{})

	resp, err := typed.Test(context.Background(), &toolingv0.TestRequest{})
	require.NoError(t, err)
	require.Equal(t, true, resp.Success,
		"bool fields must round-trip through structpb without coercion")
	require.EqualValues(t, 7, resp.TestsRun,
		"int32 counters must round-trip exactly (structpb stores numbers as float64; protojson handles the cast)")
	require.EqualValues(t, 7, resp.TestsPassed)
	require.Equal(t, "ok", resp.Output)
}

func TestBridge_RoundTrip_FindReferences_PreservesRequestEcho(t *testing.T) {
	typed, _ := startBridgedTooling(t, fakeTooling{})

	resp, err := typed.FindReferences(context.Background(), &toolingv0.FindReferencesRequest{
		File: "/abs/path/main.go", Line: 42, Column: 7,
	})
	require.NoError(t, err)
	require.Len(t, resp.Locations, 2)
	// First location echoes the request — proves typed args
	// survived the JSON↔Struct round-trip without truncation.
	require.Equal(t, "/abs/path/main.go", resp.Locations[0].File)
	require.EqualValues(t, 42, resp.Locations[0].Line)
	require.EqualValues(t, 7, resp.Locations[0].Column)
}

func TestBridge_CallTool_UnknownLangTool_ProducesActionableError(t *testing.T) {
	_, tb := startBridgedTooling(t, fakeTooling{})

	resp, err := tb.CallTool(context.Background(),
		&toolboxv0.CallToolRequest{Name: "lang.nonsense"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "lang.nonsense",
		"unknown convention tool must surface its name in the error")
	require.Contains(t, resp.Error, "ListTools")
}

func TestBridge_DirectCallTool_RawShape(t *testing.T) {
	// Confirms a Toolbox-native caller (e.g. an MCP transcoder, an
	// agent without the typed wrapper) can call lang.list_symbols
	// directly via CallTool with a Struct argument and get back
	// structured Content.
	_, tb := startBridgedTooling(t, fakeTooling{})

	args := mustStruct(t, map[string]any{"file": "raw.go"})
	resp, err := tb.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: lang.ToolListSymbols, Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "raw CallTool must succeed: %s", resp.Error)
	require.Len(t, resp.Content, 1)
	out := resp.Content[0].GetStructured().AsMap()
	syms, _ := out["symbols"].([]any)
	require.Len(t, syms, 2,
		"raw structured response must contain the same data the typed wrapper would produce")
}
