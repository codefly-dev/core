package cli

import (
	"context"
	"strings"
	"testing"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	runners "github.com/codefly-dev/core/runners/base"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestIdentityAndTools(t *testing.T) {
	tb := New(nil, "terraform", "test")
	id, err := tb.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "terraform" || len(id.CanonicalFor) != 1 || id.CanonicalFor[0] != "terraform" {
		t.Errorf("identity wrong: %+v", id)
	}
	tools := tb.Tools()
	if len(tools) != 1 || tools[0].Name != "terraform.run" || tools[0].Handler == nil {
		t.Fatalf("tools wrong: %+v", tools)
	}
}

// TestRunThroughNativeEnv exercises the real exec path: a NativeEnvironment
// provisions `echo` from PATH, the toolbox runs it and returns the (passthrough)
// output. Proves the RunnerEnvironment wiring end to end.
func TestRunThroughNativeEnv(t *testing.T) {
	ctx := context.Background()
	env, err := runners.NewNativeEnvironment(ctx, t.TempDir())
	if err != nil {
		t.Skipf("native environment unavailable: %v", err)
	}
	if err := env.Init(ctx); err != nil {
		t.Skipf("native env init: %v", err)
	}

	tb := New(env, "echo", "test")
	resp := tb.run(ctx, &toolboxv0.CallToolRequest{
		Arguments: mustArgs(t, map[string]any{"args": []any{"hello", "from", "gortk"}}),
	})
	if resp.Error != "" {
		t.Fatalf("run error: %s", resp.Error)
	}
	got := textOf(resp)
	if !strings.Contains(got, "hello from gortk") {
		t.Errorf("output = %q, want it to contain the echoed text", got)
	}
}

func mustArgs(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func textOf(resp *toolboxv0.CallToolResponse) string {
	for _, c := range resp.GetContent() {
		if txt := c.GetText(); txt != "" {
			return txt
		}
	}
	return ""
}
