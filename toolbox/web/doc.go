// Package web is the codefly Web toolbox — HTTP fetch and search
// behind a domain allowlist.
//
// This is the canonical replacement for `bash -c "curl ..."` /
// `wget ...`. Agents that need to fetch a URL or perform a web
// search call typed Tool RPCs here; the toolbox consults a
// per-call domain allowlist + a host-side default policy and
// either fetches or refuses with a clear hint.
//
// Why not let the agent shell out: a `curl` invocation in bash
// bypasses every policy decision the host wants to make about
// network egress (which domains, which methods, which auth
// headers). Routing through this toolbox means every outbound
// HTTP call lives in one auditable place, with structured
// request/response, deterministic timeout, and a sandbox that
// can deny network at the OS layer for everything else.
//
// Phase 1 ships web.fetch (one-shot HTTP) and web.search (delegated
// to a configured search provider). Phase 2 adds streaming + content
// extraction (markdown rendering of the fetched HTML, etc.).
//
// Permissions: this toolbox declares `canonical_for: [curl, wget]`.
// Sandbox: deny most reads/writes, allow network ONLY to the
// configured domain allowlist (NetworkAllowlist policy in a future
// sandbox iteration; for now it's NetworkOpen with a denylist
// enforced inside the toolbox).
package web
