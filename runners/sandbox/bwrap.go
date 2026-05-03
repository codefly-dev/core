package sandbox

import (
	"fmt"
	"os/exec"
)

// bwrapSandbox uses Linux bubblewrap to confine the child.
//
// The constructed argv looks like:
//
//	bwrap \
//	  --die-with-parent \
//	  --new-session \
//	  --proc /proc \
//	  --dev /dev \
//	  --tmpfs /tmp \
//	  --ro-bind /usr /usr  --ro-bind /lib /lib  --ro-bind /lib64 /lib64 \
//	  --ro-bind /bin /bin  --ro-bind /sbin /sbin \
//	  --ro-bind /etc /etc \
//	  --ro-bind <readPath> <readPath> ... \
//	  --bind    <writePath> <writePath> ... \
//	  --bind    <unixSocket> <unixSocket> ... \
//	  [--unshare-net]
//	  -- <originalCmd> <originalArgs...>
//
// `--die-with-parent` ensures the bwrap child gets SIGTERM if the Go
// parent dies abnormally. `--new-session` blocks TIOCSTI escapes from
// the sandboxed process onto the controlling tty. The system-image
// binds (/usr, /lib, etc.) are required because most binaries are
// dynamically linked to libraries rooted there; without them, even
// `cat` fails to start in the sandbox.
type bwrapSandbox struct {
	policy
	binary string
}

func newBwrap() (*bwrapSandbox, error) {
	bin, err := findBinary("bwrap")
	if err != nil {
		return nil, err
	}
	return &bwrapSandbox{binary: bin}, nil
}

func (s *bwrapSandbox) WithReadPaths(paths ...string) Sandbox {
	s.readPaths = append(s.readPaths, paths...)
	return s
}

func (s *bwrapSandbox) WithWritePaths(paths ...string) Sandbox {
	s.writePaths = append(s.writePaths, paths...)
	return s
}

func (s *bwrapSandbox) WithNetwork(p NetworkPolicy) Sandbox {
	s.network = p
	return s
}

func (s *bwrapSandbox) WithUnixSockets(paths ...string) Sandbox {
	s.unixSockets = append(s.unixSockets, paths...)
	return s
}

func (s *bwrapSandbox) Backend() Backend { return BackendBwrap }

// Wrap rewrites cmd to invoke bwrap with the configured policy, and
// the original argv as bwrap's payload after `--`.
//
// Refuses if cmd already appears to be wrapped (cmd.Path == s.binary).
// Double-wrapping produces nonsense like
// `bwrap ... -- bwrap ... -- orig` which would be funny if it ever ran;
// it doesn't, because bwrap-inside-bwrap fails on namespace setup.
// Better to surface it as a programmer-error here than as an obscure
// runtime failure.
//
// **NetworkLoopback on Linux.** bwrap's --unshare-net is binary —
// the new netns has every interface DOWN, including lo. To make
// loopback usable (so the codefly host's gRPC handshake to the
// plugin's loopback listener works) we wrap the inner cmd with a
// /bin/sh preamble that brings lo UP before execing the payload.
//
// Why this works without root: bwrap creates an unprivileged user
// namespace where the running uid has full capabilities inside the
// namespace. Bringing lo up inside that netns needs CAP_NET_ADMIN
// inside the namespace — which we have. /sys is not required —
// `ip link` uses an AF_NETLINK socket directly.
//
// Why /bin/sh: the wrapped exec is `bwrap ... -- /bin/sh -c
// 'ip link set lo up && exec "$@"' sh <real-cmd> <args...>`. The
// `sh -c '...' sh ...` pattern uses positional params and `exec
// "$@"`, which avoids re-quoting the original argv (no shell-escape
// hell in the wrapper).
//
// Failure mode: if /bin/sh or ip aren't on the sandbox PATH, the
// wrapped command fails immediately with the shell's own error.
// We don't pre-flight-check at Wrap time because the check itself
// would race with the namespace setup.
func (s *bwrapSandbox) Wrap(cmd *exec.Cmd) error {
	if cmd.Path == s.binary {
		return fmt.Errorf("sandbox.Wrap: cmd already wrapped by %s; constructing a fresh exec.Cmd is the supported pattern", s.binary)
	}

	args := s.buildArgs()
	args = append(args, "--")

	if s.network == NetworkLoopback {
		// Shell preamble that brings lo UP, then execs the original
		// argv. `sh -c '...' sh prog args...` puts $0=sh and $1..=
		// prog args, then exec "$@" runs the original cmd unmodified.
		args = append(args,
			"/bin/sh", "-c",
			`ip link set lo up && exec "$@"`,
			"sh", // $0 — placeholder; the shell ignores $0 in -c mode for exec
			cmd.Path,
		)
		args = append(args, cmd.Args[1:]...)
	} else {
		args = append(args, cmd.Path)
		args = append(args, cmd.Args[1:]...)
	}

	cmd.Path = s.binary
	cmd.Args = append([]string{s.binary}, args...)
	return nil
}

// ErrNetworkLoopbackUnsupported is retained for the SandboxExec
// backend's parity. It used to fire for bwrap too; that's now
// implemented (see Wrap above). Kept as an exported sentinel in
// case a future backend reverts to "unsupported."
var ErrNetworkLoopbackUnsupported = fmt.Errorf("sandbox: NetworkLoopback unsupported on this backend")

// buildArgs constructs the bwrap argv (excluding the leading binary
// name and excluding the trailing `--` + payload — those are added by
// Wrap). Exposed via tests for verification.
func (s *bwrapSandbox) buildArgs() []string {
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}

	// Standard system mounts. Read-only — programs read libraries from
	// here but should never write into them.
	for _, p := range []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc"} {
		args = append(args, "--ro-bind-try", p, p)
	}

	for _, p := range s.readPaths {
		args = append(args, "--ro-bind", p, p)
	}
	for _, p := range s.writePaths {
		args = append(args, "--bind", p, p)
	}
	for _, p := range s.unixSockets {
		args = append(args, "--bind-try", p, p)
	}

	// --unshare-net for both Deny and Loopback: a new netns with
	// every iface DOWN. The Loopback path additionally wraps the
	// payload with `ip link set lo up` (see Wrap).
	if s.network == NetworkDeny || s.network == NetworkLoopback {
		args = append(args, "--unshare-net")
	}

	return args
}
