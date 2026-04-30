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
// Returns ErrNetworkLoopbackUnsupported when NetworkLoopback is
// requested — bwrap's --unshare-net is binary, and bringing the
// loopback interface up inside the new netns requires either a
// helper binary (slirp4netns / a netlink-using preamble) or root.
// Documented as a future architectural piece in
// project_security_e2e.md; until it's implemented, callers must
// pick NetworkOpen or NetworkDeny on Linux.
func (s *bwrapSandbox) Wrap(cmd *exec.Cmd) error {
	if cmd.Path == s.binary {
		return fmt.Errorf("sandbox.Wrap: cmd already wrapped by %s; constructing a fresh exec.Cmd is the supported pattern", s.binary)
	}
	if s.network == NetworkLoopback {
		return ErrNetworkLoopbackUnsupported
	}

	args := s.buildArgs()
	args = append(args, "--")
	args = append(args, cmd.Path)
	args = append(args, cmd.Args[1:]...)

	cmd.Path = s.binary
	cmd.Args = append([]string{s.binary}, args...)
	return nil
}

// ErrNetworkLoopbackUnsupported is returned by the bwrap backend when
// NetworkLoopback is requested. macOS sandbox-exec supports it; Linux
// support is gated on a netns-loopback helper that hasn't been
// implemented yet. Callers handle this by either falling back to
// NetworkOpen (less secure but functional) or refusing to start the
// plugin (most secure but blocks Linux users until the helper lands).
var ErrNetworkLoopbackUnsupported = fmt.Errorf("sandbox: NetworkLoopback not implemented on Linux bwrap backend (need lo-up helper inside unshared netns); pick NetworkOpen or NetworkDeny, or implement the wrapper — see project_security_e2e.md")

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

	if s.network == NetworkDeny {
		args = append(args, "--unshare-net")
	}

	return args
}
