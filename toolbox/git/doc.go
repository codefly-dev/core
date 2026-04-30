// Package git is the codefly Git toolbox: every git operation an
// agent might want to perform, exposed as typed Tool RPCs through
// the codefly.services.toolbox.v0 contract.
//
// This is the canonical replacement for `bash -c "git ..."` —
// agents that need to interact with a repository call a Tool here
// (git.status, git.log, git.diff, ...) and get structured results
// back. The Bash toolbox refuses any `git` invocation by referring
// the caller to this toolbox via the canonical-binary registry.
//
// Implementation uses go-git (github.com/go-git/go-git/v5) — pure
// Go, no shell-out, no /usr/bin/git dependency. That's what makes
// the OS-level sandbox tight: even if the bash parser is fooled,
// the spawned shell can't reach a git binary because none was
// granted into the sandbox.
//
// Phase 1 ships a minimal tool set proving the contract integration:
// status, log, diff. Add tools as needed; the dispatcher in
// CallTool is a switch on tool name, the input schemas are inline.
//
// Permissions: this toolbox declares `canonical_for: [git]` in its
// manifest. Sandbox: read+write the workspace root, deny network
// (push/pull come later with explicit network grants).
package git
