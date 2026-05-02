//go:build sandbox_e2e

// Build-tagged: these tests require a working OS sandbox backend
// (bwrap on Linux, sandbox-exec on macOS) AND issue real network
// fetches. They prove the security stack composes end-to-end and
// are slow + dependency-heavy. CI matrices that have the backends
// installed run with `go test -tags sandbox_e2e ./...`. The
// no-tag default skips them at compile time — which satisfies
// the no-t.Skip rule (skipping is the right behavior for a missing
// system dep; doing it via a build tag rather than t.Skip means
// the absence is visible in the build configuration, not silent
// at runtime).
package launch_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/toolbox/launch"
)

// installVictimAtAgentPath compiles the network-victim plugin and
// installs it at the canonical agent path under codeflyHome.
func installVictimAtAgentPath(t *testing.T, ctx context.Context, codeflyHome string, ag *resources.Agent) {
	t.Helper()
	t.Setenv(resources.CodeflyHomeEnv, codeflyHome)

	target, err := ag.Path(ctx)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))

	cmd := exec.Command("go", "build", "-o", target,
		"github.com/codefly-dev/core/toolbox/launch/cmd/network-victim-toolbox")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build network-victim failed:\n%s", out)
}

// requireEnforcingBackend asserts a real OS sandbox backend is
// available and fails the test loud if not (no t.Skip per the rule).
func requireEnforcingBackend(t *testing.T) sandbox.Sandbox {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Fatalf("OS sandbox enforcement requires macOS (sandbox-exec) or Linux (bwrap); current: %s. Run on a CI matrix that includes one.", runtime.GOOS)
	}
	sb, err := sandbox.New()
	require.NoError(t, err, "sandbox backend missing — install bwrap (Linux) or run on macOS")
	require.NotEqual(t, sandbox.BackendNative, sb.Backend(),
		"this test requires an enforcing backend (got native); native is opt-out")
	return sb
}

// TestE2E_OSSandbox_BlocksWriteOutsideAllowedPaths is the
// load-bearing security composition test. A plugin spawned via
// launch.Launch declares write_paths=[allowedDir]; an attempt to
// write OUTSIDE allowedDir must be blocked by the OS sandbox.
//
// Why this test matters: every prior layer (canonical registry, PDP,
// application-side allowlists) operates at the application boundary.
// The OS sandbox is the LAST line of defense — if a plugin's
// application-layer code is bypassed (a bug, an unguarded path,
// argument injection), the sandbox is what stops it. Until this
// test was wired, the OS-sandbox layer was defined but not
// applied at plugin spawn time.
//
// Why FS write instead of network: the sandbox's deny-network
// today is binary (--unshare-net on Linux) which ALSO breaks the
// plugin's loopback gRPC handshake. A NetworkLoopback policy that
// keeps 127.0.0.1 reachable while denying outbound is a real
// architectural follow-up; for now we exercise the OS-layer
// enforcement via filesystem writes, which is equally load-bearing
// and doesn't conflict with the handshake.
func TestE2E_OSSandbox_BlocksWriteOutsideAllowedPaths(t *testing.T) {
	requireEnforcingBackend(t)

	// macOS gotcha: t.TempDir() returns "/var/folders/..." but the
	// real path is "/private/var/folders/..." (the former is a
	// symlink). The sandbox profile matches on the literal path
	// string, so we must resolve symlinks BEFORE handing paths to
	// the policy. Otherwise the plugin's resolved paths don't
	// match the rule and even the "allowed" write fails.
	codeflyHome := resolveSymlinks(t, t.TempDir())
	allowedWriteDir := resolveSymlinks(t, t.TempDir())

	// forbiddenWriteDir is allocated OUTSIDE the test's t.TempDir()
	// subtree on purpose. On Linux, t.TempDir() returns paths under
	// /tmp/ — the same /tmp/ that go's runtime scribbles into via
	// $TMPDIR. If we put the forbidden dir under t.TempDir AND
	// granted ${TMPDIR} in WritePaths (for go runtime), the
	// "forbidden" path would actually be allowed, masking real
	// sandbox bypasses. Putting forbidden under /var/tmp keeps it
	// outside any TMPDIR grant.
	forbiddenWriteDir, err := os.MkdirTemp("/var/tmp", "codefly-forbidden-")
	require.NoError(t, err)
	defer os.RemoveAll(forbiddenWriteDir)
	forbiddenWriteDir = resolveSymlinksOptional(t, forbiddenWriteDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-fswrite",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-fswrite",
		},
		// Non-empty CanonicalFor defeats the isEmptyPolicy escape
		// hatch in launch.buildSandbox; the manifest is asserting
		// authority and so MUST be sandboxed.
		CanonicalFor: []string{"fs-victim"},
		Sandbox: policy.SandboxPolicy{
			// Everything the plugin needs to BOOT — Go runtime
			// needs to read the binary, libraries, /proc, /sys
			// (Linux), and TMPDIR for go's own scratch.
			ReadPaths: []string{
				"${HOME}",
				"${TMPDIR}",
				codeflyHome, // the agent install path
			},
			// Writes ONLY allowed in allowedWriteDir. forbidden
			// stays unwritable.
			WritePaths: []string{
				allowedWriteDir,
				"${TMPDIR}", // go's runtime may scribble in tmp
			},
			Network: policy.NetworkOpen, // keep loopback gRPC working
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{Workspace: ""})
	require.NoError(t, err, "victim plugin must spawn under sandbox; we test that it BLOCKS specific writes, not boot")
	defer plugin.Close()

	// gRPC must still work — the sandbox grants loopback when
	// network is Open. (Deny-network would break this; that's the
	// architectural limitation noted above.)
	_, err = plugin.Client.Identity(ctx, &toolboxv0.IdentityRequest{})
	require.NoError(t, err)

	// --- Allowed write — must SUCCEED ---
	allowedTarget := filepath.Join(allowedWriteDir, "ok.txt")
	args, _ := structpb.NewStruct(map[string]any{"path": allowedTarget, "content": "hello\n"})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "fs.write", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"writing to an allowed path must succeed under the sandbox: %s", resp.Error)
	// Confirm the file actually appeared on disk — the sandbox
	// could have lied about success.
	_, err = os.Stat(allowedTarget)
	require.NoError(t, err, "allowed write must produce the file on disk")

	// --- Forbidden write — must FAIL at OS layer ---
	forbiddenTarget := filepath.Join(forbiddenWriteDir, "blocked.txt")
	args2, _ := structpb.NewStruct(map[string]any{"path": forbiddenTarget, "content": "should-not-exist\n"})
	resp2, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "fs.write", Arguments: args2,
	})
	require.NoError(t, err, "gRPC must succeed; the WRITE inside the plugin is what fails")
	require.NotEmpty(t, resp2.Error,
		"OS sandbox MUST block the forbidden write — got success: %s", contentSummary(resp2))
	require.Contains(t, resp2.Error, "write failed",
		"the failure must surface from the plugin's error wrap")

	// Sanity: error indicates a sandbox-level blockage. Two
	// equivalent shapes:
	//   - macOS sandbox-exec returns EACCES / "operation not
	//     permitted" because the path exists but the policy denies.
	//   - bwrap (Linux) hides the path entirely from the new mount
	//     namespace, so open() returns ENOENT — "no such file or
	//     directory". Either is a successful block; the file MUST
	//     NOT have been written (the os.Stat assertion below is the
	//     deepest proof).
	combined := strings.ToLower(resp2.Error)
	hasFSDenialSignal := strings.Contains(combined, "permission denied") ||
		strings.Contains(combined, "operation not permitted") ||
		strings.Contains(combined, "read-only") ||
		strings.Contains(combined, "denied") ||
		strings.Contains(combined, "no such file or directory") // bwrap mount-namespace hide
	require.True(t, hasFSDenialSignal,
		"error must indicate a sandbox-level blockage (got: %q)", resp2.Error)

	// AND the file must NOT exist on disk — the deepest assertion.
	// If the sandbox merely returned an error but the bytes hit the
	// disk, that's a sandbox bypass and the test should fail.
	_, statErr := os.Stat(forbiddenTarget)
	require.True(t, os.IsNotExist(statErr),
		"forbidden write target must NOT exist on disk; if it does, the sandbox failed to block (stat err: %v)", statErr)
}

// TestE2E_NoSandbox_AllowsForbiddenWrite is the negative control:
// when the manifest declares NO sandbox policy AND no canonical_for,
// the launch layer skips wrapping. The same victim plugin then
// SUCCEEDS at writing anywhere — proving the blocking in the
// previous test came from the sandbox, not from the plugin's own
// behavior.
func TestE2E_NoSandbox_AllowsForbiddenWrite(t *testing.T) {
	codeflyHome := t.TempDir()
	writeDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-control",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-control",
		},
		// No CanonicalFor + empty Sandbox → isEmptyPolicy = true →
		// no sandbox wrap. Plugin runs unconfined.
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.Launch(ctx, tb)
	require.NoError(t, err)
	defer plugin.Close()

	target := filepath.Join(writeDir, "control.txt")
	args, _ := structpb.NewStruct(map[string]any{"path": target, "content": "control\n"})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "fs.write", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"unsandboxed control write must succeed — proves the plugin itself works without a sandbox; if this fails, see negative-control note")

	_, err = os.Stat(target)
	require.NoError(t, err, "control file must exist on disk")
}

// The Loopback-policy E2E test (network-handshake counterpart of
// the write-blocking test) lives in sandbox_e2e_loopback_darwin_test.go
// — darwin-only because bwrap (Linux) doesn't support
// NetworkLoopback yet. See project_security_e2e.md for the netns
// follow-up.

// resolveSymlinks returns the symlink-resolved absolute path. On
// macOS t.TempDir paths are symlinks; the sandbox profile matches
// the resolved real path, so callers MUST normalize before declaring.
func resolveSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err, "resolve symlinks for %s", path)
	return resolved
}

// resolveSymlinksOptional is like resolveSymlinks but tolerates
// already-resolved paths or paths whose intermediate components
// don't exist as symlinks. Used for /var/tmp paths which may or may
// not be symlink-resolved depending on the OS.
func resolveSymlinksOptional(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// contentSummary stringifies a CallToolResponse's content blocks for
// failure messages.
func contentSummary(resp *toolboxv0.CallToolResponse) string {
	parts := make([]string, 0, len(resp.GetContent()))
	for _, c := range resp.GetContent() {
		if t := c.GetText(); t != "" {
			parts = append(parts, t)
			continue
		}
		if s := c.GetStructured(); s != nil {
			parts = append(parts, s.String())
		}
	}
	return strings.Join(parts, " | ")
}
