// Package bash is the codefly Bash toolbox — the only sanctioned path
// for running shell commands across the codefly stack.
//
// Two layers of enforcement, applied in order:
//
//  1. AST-level canonical routing. Every script is parsed with
//     mvdan/sh; each CallExpr's program name is looked up in the
//     policy.CanonicalRegistry. If the binary is owned by a
//     dedicated toolbox (Git, Docker, Nix, …), the script is
//     refused with an error that names the toolbox the caller
//     should use instead. Splitting on `&&`, `||`, `;`, `|`, and
//     subshells happens by construction — every Cmd node is visited.
//     Defeats the canonical "git status && git push" chaining bypass
//     that defeats every existing token-level permission system.
//
//  2. OS sandbox. Even an allowed command runs inside a
//     runners/sandbox wrap with no access to the canonically-denied
//     binaries: bwrap doesn't bind /usr/bin/git, sandbox-exec's
//     profile leaves the binary unreachable. So even if the parser
//     is somehow fooled (a trick we haven't considered), the
//     spawned bash literally cannot find the program to run.
//
// The two layers are independent. Either one alone would be
// bypassable; together they form the contract.
package bash
