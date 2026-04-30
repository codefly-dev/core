// Package nix is the codefly Nix toolbox — flake introspection and
// evaluation exposed as typed Tool RPCs.
//
// This is the canonical replacement for `bash -c "nix ..."`. Agents
// that need to inspect a flake, list its outputs, or evaluate a nix
// expression call typed RPCs here; the Bash toolbox refuses every
// `nix` invocation and routes callers via canonical_for: [nix].
//
// Implementation shells out to the nix binary (no pure-Go nix
// evaluator exists). That's fine: the Nix toolbox is the canonical
// owner of the binary, so the parser layer routes here, and the OS
// sandbox grants the nix binary specifically into THIS toolbox's
// sandbox — not into the bash toolbox's, where it would otherwise
// be unreachable. This is the architectural payoff: each toolbox
// has its own sandbox, scoped to exactly the binaries it claims.
//
// Phase 1 ships a minimal read-only set:
//   - nix.flake_metadata — `nix flake metadata --json` on a flake
//   - nix.flake_show     — `nix flake show --json` (outputs surface)
//   - nix.eval           — `nix eval --json` of an expression
//
// Mutation tools (build, develop, run) come later — they need
// careful thinking about resource caps (a `nix build` can fetch
// gigabytes) and about the boundary between the toolbox and the
// existing runners/base.NixEnvironment which manages devshells for
// service plugins.
//
// Permissions: this toolbox declares `canonical_for: [nix]`.
// Sandbox: read-only by default, network granted to the nix
// substituters configured in the host (cache.nixos.org typically),
// writes scoped to /nix/store + the materialization cache dir.
package nix
