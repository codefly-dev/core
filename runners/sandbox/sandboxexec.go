package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// getenv is split out for tests; mirrors os.Getenv.
var getenv = os.Getenv

// sandboxExecSandbox uses macOS Seatbelt via /usr/bin/sandbox-exec.
//
// Despite Apple deprecating the CLI, sandbox-exec remains the only
// realistic ad-hoc Seatbelt entrypoint — App Sandbox itself is
// unworkable for command-line tools. The daemon and bundle alternatives
// require a signed .app and entitlements.
//
// The generated profile is conservative: deny default, then enable the
// minimum needed for a child process to start (process-fork/exec,
// signal-self) and read system frameworks. Per-policy adds layer on
// top.
type sandboxExecSandbox struct {
	policy
	binary string
}

func newSandboxExec() (*sandboxExecSandbox, error) {
	bin, err := findBinary("sandbox-exec")
	if err != nil {
		return nil, err
	}
	return &sandboxExecSandbox{binary: bin}, nil
}

func (s *sandboxExecSandbox) WithReadPaths(paths ...string) Sandbox {
	s.readPaths = append(s.readPaths, paths...)
	return s
}

func (s *sandboxExecSandbox) WithWritePaths(paths ...string) Sandbox {
	s.writePaths = append(s.writePaths, paths...)
	return s
}

func (s *sandboxExecSandbox) WithNetwork(p NetworkPolicy) Sandbox {
	s.network = p
	return s
}

func (s *sandboxExecSandbox) WithUnixSockets(paths ...string) Sandbox {
	s.unixSockets = append(s.unixSockets, paths...)
	return s
}

func (s *sandboxExecSandbox) Backend() Backend { return BackendSandboxExec }

// Wrap rewrites cmd to invoke `sandbox-exec -p <profile>` with the
// generated profile inline.
func (s *sandboxExecSandbox) Wrap(cmd *exec.Cmd) error {
	profile, err := s.buildProfile()
	if err != nil {
		return err
	}

	args := []string{"-p", profile, cmd.Path}
	args = append(args, cmd.Args[1:]...)

	cmd.Path = s.binary
	cmd.Args = append([]string{s.binary}, args...)
	return nil
}

// buildProfile assembles the .sb profile string.
//
// Threat model (ranked):
//
//  1. Writes outside the workspace — corruption / exfiltration.
//  2. Outbound network — exfiltration / supply-chain.
//  3. Reads of host secrets (.ssh, .aws, .gitconfig).
//
// Strategy: start permissive on reads (Apple's dyld + syscall path is
// brittle under deny-default; cat-with-allowed-readpath fights every
// /System/Cryptexes / mach lookup), then explicitly deny known-secret
// roots. WRITES are deny-default with allowlist, NETWORK is deny by
// default. This matches the actual threat surface and avoids the
// "deny-default Seatbelt fights every libdyld cache lookup" rabbit hole.
func (s *sandboxExecSandbox) buildProfile() (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	// --- Writes: deny by default, then allowlist declared paths ---
	b.WriteString("(deny file-write*)\n")
	// /dev/null, /dev/tty, /dev/stdout, /dev/stderr are always
	// writable — they're how processes signal failure or print output;
	// denying them turns every diagnostic into a write violation.
	b.WriteString(`(allow file-write-data (literal "/dev/null"))` + "\n")
	b.WriteString(`(allow file-write-data (literal "/dev/tty"))` + "\n")
	b.WriteString(`(allow file-write-data (literal "/dev/stdout"))` + "\n")
	b.WriteString(`(allow file-write-data (literal "/dev/stderr"))` + "\n")
	for _, p := range s.writePaths {
		if p == "" {
			return "", fmt.Errorf("empty write path")
		}
		fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", p)
	}

	// --- Reads: explicit deny for known-secret roots ---
	if home := homeDir(); home != "" {
		for _, secret := range []string{".ssh", ".aws", ".config/codefly/secrets", ".gnupg"} {
			fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", home+"/"+secret)
		}
	}

	// --- Network: deny by default; opt-in via NetworkOpen ---
	if s.network != NetworkOpen {
		b.WriteString("(deny network*)\n")
	}

	// --- Unix-socket allowlist (network-outbound by path regex) ---
	for _, p := range s.unixSockets {
		if p == "" {
			return "", fmt.Errorf("empty unix-socket path")
		}
		fmt.Fprintf(&b, "(allow network-outbound (regex #\"^%s\"))\n", regexQuote(p))
	}

	// readPaths are advisory on macOS under this model: file-read* is
	// already broadly allowed. We still record them for parity with the
	// bwrap backend (where they DO carry weight) and for diagnostics.
	for _, p := range s.readPaths {
		if p == "" {
			return "", fmt.Errorf("empty read path")
		}
		// no-op rule; harmless and documents intent in the profile
		fmt.Fprintf(&b, "; declared read-path: %q\n", p)
	}

	return b.String(), nil
}

// homeDir resolves $HOME for the secret-deny rules. Returns "" if
// unset, in which case the secret-deny rules are skipped — the policy
// is best-effort hardening, not a security boundary on its own.
func homeDir() string {
	return getenv("HOME")
}

// regexQuote escapes Seatbelt regex metacharacters in a literal path.
func regexQuote(p string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`.`, `\.`,
		`+`, `\+`,
		`*`, `\*`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`|`, `\|`,
		`^`, `\^`,
		`$`, `\$`,
	)
	return r.Replace(p)
}
