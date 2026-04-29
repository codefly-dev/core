// Package policy is the codefly permission and capability layer.
//
// It encodes who-can-do-what across plugins:
//
//   - SandboxPolicy: the read/write/network/socket policy a plugin
//     declares in its manifest. Translates 1:1 to a runners/sandbox
//     configuration.
//
//   - CanonicalRegistry: the binary→toolbox map used to enforce
//     "every plugin's Bash that tries to run `git` is denied; route
//     through the Git toolbox." Computed from the union of plugins'
//     `canonical_for:` declarations plus a small built-in fallback
//     for binaries no toolbox has claimed yet (so that even before
//     the Git toolbox ships, `bash -c git` is blocked).
//
// Enforcement happens at two layers in concert:
//
//  1. AST-aware bash parsing (mvdan/sh) splits on &&/||/;/| and
//     evaluates each command independently against the registry.
//     Defeats the canonical "git status && git push" chaining bypass
//     that every other coding agent permission system has.
//
//  2. OS sandbox (runners/sandbox): even if the parser is fooled, the
//     bash executor itself runs inside bwrap/sandbox-exec with no
//     access to git/docker/nix binaries. Belt-and-suspenders — both
//     layers must fail for an agent to break out.
//
// Permissions live IN THE PLUGIN MANIFEST (declarative), not in
// codefly host code. The host reads, validates, builds the registry,
// and enforces. Plugins are the source of truth.
package policy
