// Package sandbox confines a child process's filesystem and network
// access using OS-level primitives.
//
// Backends:
//
//   - native      — no-op pass-through. For tests and explicit opt-out.
//   - bwrap       — bubblewrap on Linux. Mounts a fresh root with
//                   allowlisted read/write binds, optional --unshare-net.
//   - sandboxexec — macOS Seatbelt via `sandbox-exec` and a generated
//                   .sb profile. Apple has deprecated the CLI but still
//                   uses it internally for App Sandbox; no replacement
//                   covers ad-hoc CLI sandboxing.
//
// API shape mirrors Anthropic's sandbox-runtime: declare read paths,
// write paths, network policy, unix-socket allowlist, then call Wrap on
// a prepared exec.Cmd. The wrap is one-way — the caller drives Start /
// Wait as usual.
//
//	sb, err := sandbox.New()  // bwrap on Linux, sandbox-exec on macOS
//	sb.WithReadPaths("/etc", "/usr").
//	   WithWritePaths(workDir).
//	   WithNetwork(sandbox.NetworkDeny)
//	cmd := exec.Command("bash", "-c", "...")
//	if err := sb.Wrap(cmd); err != nil { return err }
//	return cmd.Run()
//
// Threat model: this package defends against accidental scope creep
// from tools spawned by an LLM agent — writes outside the workspace,
// surprise network calls, reads of $HOME secrets. It is NOT a defense
// against a hostile binary that's actively trying to escape; both
// bwrap and Seatbelt have known bypasses for unprivileged code that
// the kernel can't fully isolate (CVE history exists for both).
package sandbox
