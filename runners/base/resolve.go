package base

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// ResolveStandaloneEnvironment returns a RunnerEnvironment that honors
// the plugin's declared RuntimeContext when possible, for callers that
// need a runner but are not plumbed through Runtime.CreateRunnerEnvironment.
//
// Used by Code / Tooling when their shared Service.ActiveEnv is nil —
// typically when a Code RPC is served before Runtime.Init has run. The
// fallback chain:
//
//  1. `ctx.Kind == Nix` AND nix is installed AND flake.nix is in dir → NixEnvironment.
//  2. `ctx.Kind == Container` → **native fallback with caveat**. Code at
//     the generic layer does not know the plugin's RuntimeImage (that's
//     declared per-plugin in main.go), so we can't spin a Docker env
//     here. Formatters and AST tools operate on host files and produce
//     deterministic output — running them natively is acceptable. Use
//     ActiveEnv (set by Init) when you need full mode consistency.
//  3. Anything else → NativeEnvironment.
//
// Never returns nil. Errors creating Nix fall through to native.
func ResolveStandaloneEnvironment(ctx context.Context, dir string, runtimeCtx *basev0.RuntimeContext) RunnerEnvironment {
	if runtimeCtx != nil {
		if runtimeCtx.Kind == resources.RuntimeContextNix && CheckNixInstalled() && IsNixSupported() {
			if nix, err := NewNixEnvironment(ctx, dir); err == nil {
				return nix
			}
		}
	}
	if native, err := NewNativeEnvironment(ctx, dir); err == nil {
		return native
	}
	// Extremely rare — NewNativeEnvironment only fails on wool context
	// init which is effectively infallible. Return a freshly-constructed
	// struct so callers never nil-deref; Init will no-op.
	return &NativeEnvironment{dir: dir}
}
