// Command fake-lang-toolbox is a TEST-ONLY language plugin used by
// the round-trip integration test in core/toolbox/lang. It exposes
// a Toolbox via lang.NewToolboxFromTooling over a fake Tooling impl
// with deterministic outputs.
//
// Real language plugins (python, go, rust) follow the same shape:
//
//	tooling := pythontooling.New(...)
//	agents.Serve(agents.PluginRegistration{
//	    ...,
//	    Toolbox: lang.NewToolboxFromTooling(name, version, tooling),
//	})
//
// This binary's only job is to be small enough to build fast in
// tests while exercising every layer (handshake, gRPC server,
// bridge, typed wrapper).
package main

import (
	"context"
	"os"

	"github.com/codefly-dev/core/agents"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"github.com/codefly-dev/core/toolbox/lang"
)

// fakeTooling returns deterministic fixtures so the round-trip test
// can assert exact equality. Every RPC the test exercises is
// implemented; the rest fall through to UnimplementedToolingServer
// (which surfaces as gRPC Unimplemented — the bridge passes that
// through cleanly).
type fakeTooling struct {
	toolingv0.UnimplementedToolingServer
	version string
}

func (f *fakeTooling) ListSymbols(_ context.Context, req *toolingv0.ListSymbolsRequest) (*toolingv0.ListSymbolsResponse, error) {
	return &toolingv0.ListSymbolsResponse{
		Symbols: []*toolingv0.Symbol{
			{Name: "alpha", QualifiedName: req.File + ":alpha"},
			{Name: "beta", QualifiedName: req.File + ":beta"},
		},
	}, nil
}

func (f *fakeTooling) Test(_ context.Context, _ *toolingv0.TestRequest) (*toolingv0.TestResponse, error) {
	return &toolingv0.TestResponse{
		Success:     true,
		TestsRun:    3,
		TestsPassed: 3,
		Output:      "fake: 3/3 ok",
	}, nil
}

func main() {
	version := os.Getenv("CODEFLY_TOOLBOX_VERSION")
	if version == "" {
		version = "0.0.0-fake"
	}
	tooling := &fakeTooling{version: version}
	agents.Serve(agents.PluginRegistration{
		Toolbox: lang.NewToolboxFromTooling("fake-lang", version, tooling),
	})
}
