package dependencies

import (
	"context"
	"slices"
	"testing"
)

func TestPortAllocationModeIsForwardedAndIsolatesReusableStacks(t *testing.T) {
	defaultMode := &Option{}
	namedMode := &Option{}
	WithTemporaryPorts(false)(namedMode)
	if !slices.Contains(dependencyCommandArguments(defaultMode, "fixture"), "--temporary-ports") {
		t.Fatal("default must preserve disposable ports")
	}
	args := dependencyCommandArguments(namedMode, "fixture")
	if !slices.Contains(args, "--temporary-ports=false") || slices.Contains(args, "--temporary-ports") {
		t.Fatalf("named mode: %v", args)
	}
	for _, required := range []string{"fixture", "--naming-scope", "--exclude-root", "--cli-server", "--headless"} {
		if !slices.Contains(args, required) {
			t.Fatalf("mode lost %s: %v", required, args)
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	if reuseFingerprint(ctx, dir, defaultMode) == reuseFingerprint(ctx, dir, namedMode) {
		t.Fatal("different allocation modes share a reusable stack")
	}
	explicit := &Option{}
	WithTemporaryPorts(true)(explicit)
	if reuseFingerprint(ctx, dir, defaultMode) != reuseFingerprint(ctx, dir, explicit) {
		t.Fatal("explicit default changes stack identity")
	}
}
