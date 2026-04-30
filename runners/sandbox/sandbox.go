package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
)

// NetworkPolicy controls the child process's network access.
type NetworkPolicy int

const (
	// NetworkDeny severs the child's network entirely (Linux:
	// --unshare-net; macOS: deny network*). Default for new sandboxes.
	NetworkDeny NetworkPolicy = iota

	// NetworkOpen leaves networking unrestricted. Explicit opt-in only,
	// because nothing surfaces "this tool just made an outbound call."
	NetworkOpen

	// NetworkLoopback allows local 127.0.0.1 traffic only — the
	// plugin can bind/connect on loopback (so its gRPC handshake to
	// the parent works) but EVERY outbound or external connection
	// is blocked by the OS sandbox.
	//
	// This is the secure-by-default policy for codefly plugins:
	// they need loopback for the agent handshake, and they should
	// NOT be making outbound calls without an explicit grant.
	//
	// Backend status:
	//   macOS sandbox-exec — implemented (rule on localhost ip).
	//   Linux bwrap        — NOT implemented; needs `ip link set lo up`
	//                        inside the unshared netns (the new netns
	//                        has lo DOWN by default). Falls back to
	//                        a clear error from Wrap so callers see
	//                        the limitation rather than silent
	//                        under/over-permissive behavior.
	NetworkLoopback
)

// Backend identifies which sandbox implementation is in use.
type Backend string

const (
	BackendNative      Backend = "native"
	BackendBwrap       Backend = "bwrap"
	BackendSandboxExec Backend = "sandboxexec"
)

// Sandbox confines a child process's filesystem and network access.
//
// Path declarations are advisory in the sense that the caller is
// responsible for declaring everything the child legitimately needs;
// missing declarations cause runtime denials, not silent breakage. Read
// paths grant ancestor traversal automatically (bwrap models this; the
// macOS profile mirrors it).
//
// Sandboxes are single-use. Wrap mutates cmd.Args / cmd.Path — calling
// Wrap on the same cmd twice will produce nonsense.
type Sandbox interface {
	// WithReadPaths grants read-only access to absolute paths.
	WithReadPaths(paths ...string) Sandbox

	// WithWritePaths grants read+write access to absolute paths.
	WithWritePaths(paths ...string) Sandbox

	// WithNetwork sets the network policy. Default is NetworkDeny.
	WithNetwork(policy NetworkPolicy) Sandbox

	// WithUnixSockets allows access to specific unix socket paths.
	// Distinct from WithReadPaths because socket access on macOS is a
	// separate Seatbelt category from filesystem read.
	WithUnixSockets(paths ...string) Sandbox

	// Wrap takes a prepared exec.Cmd and rewrites it to run inside the
	// sandbox. The original cmd.Path / cmd.Args become the wrapped
	// command's payload. Stdin/Stdout/Stderr, Env, Dir, and SysProcAttr
	// are preserved.
	//
	// On native (no-op) the cmd is returned unmodified.
	//
	// Returns an error if the backend is unavailable (bwrap not on
	// PATH, sandbox-exec missing) or the policy is malformed.
	Wrap(cmd *exec.Cmd) error

	// Backend returns the backend in use, for diagnostics.
	Backend() Backend
}

// New returns the appropriate sandbox for the current OS:
//
//   - Linux  → bwrap (requires `bwrap` on PATH)
//   - darwin → sandbox-exec (always present on macOS)
//   - other  → native no-op
//
// Use NewNative() explicitly when the caller has authorized
// unrestricted exec — the callsite is auditable.
func New() (Sandbox, error) {
	switch runtime.GOOS {
	case "linux":
		return newBwrap()
	case "darwin":
		return newSandboxExec()
	default:
		return NewNative(), nil
	}
}

// NewNative returns a no-op sandbox. Tests and explicit opt-out only.
func NewNative() Sandbox {
	return &nativeSandbox{}
}

// findBinary returns the absolute path of name, or an error suitable
// for surfacing "your sandbox backend isn't installed."
func findBinary(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH (required by sandbox backend): %w", name, err)
	}
	return p, nil
}

// policy is the shared, immutable-in-spirit configuration carried by
// every backend. Backends translate it to bwrap args / sandbox-exec
// profile strings at Wrap time.
type policy struct {
	readPaths    []string
	writePaths   []string
	network      NetworkPolicy
	unixSockets  []string
}
