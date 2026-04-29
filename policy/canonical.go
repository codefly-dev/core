package policy

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// CanonicalRegistry maps a binary name to the toolbox that owns it.
//
// When a plugin's Bash toolbox parses a command and finds the leaf
// program is in this registry, it MUST refuse and tell the agent to
// invoke the canonical toolbox instead. The canonical-tool routing
// is the high-level enforcement layer — the OS sandbox is the
// belt-and-suspenders below it.
//
// Population:
//
//   - At plugin manifest load time, every plugin's `canonical_for:`
//     list contributes to the registry. Each binary name may have
//     exactly one canonical owner; a conflict is a load-time error
//     (caught early, not at first invocation).
//
//   - A built-in fallback covers binaries no plugin has claimed yet
//     (`git`, `docker`, `nix`, `kubectl`, `helm`, `curl`, `wget`).
//     Default behavior for the fallback set: DenyMissingToolbox —
//     refuse with a clear "install the X toolbox" hint, instead of
//     silently letting bash run the binary unsupervised.
type CanonicalRegistry struct {
	mu       sync.RWMutex
	bindings map[string]canonicalBinding
}

// canonicalBinding records which plugin claims a binary as its
// canonical toolbox. Owner is empty for built-in fallbacks (no plugin
// has claimed it yet).
type canonicalBinding struct {
	Owner  string // plugin name or "" for built-in fallback
	Reason string // human-readable note for diagnostics / error messages
}

// builtinFallback covers binaries that, when invoked from a generic
// Bash toolbox, are categorically a security concern: they have a
// dedicated typed surface that should be used instead.
//
//	git    → use the Git toolbox (commit/push/pull as typed RPCs)
//	docker → use the Docker toolbox (image/container ops)
//	nix    → use the Nix toolbox (flake eval, devshell)
//	kubectl, helm → cluster ops; should be a Kubernetes toolbox
//	curl, wget → use the Web toolbox (with domain allowlists)
//
// If a plugin explicitly claims one of these (CanonicalFor: [git]),
// its claim wins; the fallback is only consulted for unclaimed
// binaries.
var builtinFallback = map[string]canonicalBinding{
	"git":     {Reason: "use the Git toolbox; bash-with-git bypasses repo-scoped permissions"},
	"docker":  {Reason: "use the Docker toolbox; bash-with-docker bypasses image-pull and container-spawn audit"},
	"nix":     {Reason: "use the Nix toolbox; bash-with-nix bypasses flake evaluation policy"},
	"kubectl": {Reason: "use the Kubernetes toolbox; bash-with-kubectl bypasses cluster-scope policy"},
	"helm":    {Reason: "use the Kubernetes toolbox; bash-with-helm bypasses release audit"},
	"curl":    {Reason: "use the Web toolbox; bash-with-curl bypasses domain allowlist"},
	"wget":    {Reason: "use the Web toolbox; bash-with-wget bypasses domain allowlist"},
}

// NewCanonicalRegistry returns a registry seeded with the built-in
// fallback. Plugin claims are added via Claim.
func NewCanonicalRegistry() *CanonicalRegistry {
	r := &CanonicalRegistry{bindings: make(map[string]canonicalBinding, len(builtinFallback))}
	for k, v := range builtinFallback {
		r.bindings[k] = v
	}
	return r
}

// Claim records that `owner` is the canonical toolbox for each
// binary in `binaries`. Returns an error if any binary already has a
// non-fallback owner — two plugins both claiming `git` is a
// configuration error that must surface at load time, not at first
// invocation.
//
// If an existing entry is a built-in fallback (owner == ""), the
// claim wins silently — the Git toolbox plugin claims `git`,
// replacing the unclaimed-fallback entry.
func (r *CanonicalRegistry) Claim(owner string, binaries ...string) error {
	if owner == "" {
		return fmt.Errorf("canonical claim requires a non-empty owner")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range binaries {
		if b == "" {
			return fmt.Errorf("canonical claim by %q includes an empty binary name", owner)
		}
		existing, ok := r.bindings[b]
		if ok && existing.Owner != "" && existing.Owner != owner {
			return fmt.Errorf("binary %q already claimed by plugin %q; cannot also claim for %q",
				b, existing.Owner, owner)
		}
		r.bindings[b] = canonicalBinding{
			Owner:  owner,
			Reason: fmt.Sprintf("canonical toolbox for %q is plugin %q", b, owner),
		}
	}
	return nil
}

// Decision is the result of looking up a binary in the registry.
type Decision struct {
	// Routed indicates the binary has a canonical toolbox; the bash
	// executor must refuse and direct the caller there.
	Routed bool

	// Owner is the plugin name that owns the canonical surface, or ""
	// for the built-in fallback (no plugin yet ships the toolbox; the
	// binary is denied with a hint to install one).
	Owner string

	// Reason is the human-readable explanation for the routing —
	// suitable for surfacing verbatim in the bash-toolbox error.
	Reason string
}

// Lookup returns the routing decision for a binary name. A nil
// decision means the binary is not routed and bash may execute it
// (subject to whatever other policy layers apply). The lookup
// strips a leading path: `/usr/bin/git` resolves to `git`.
func (r *CanonicalRegistry) Lookup(bin string) *Decision {
	bin = leafName(bin)
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bindings[bin]
	if !ok {
		return nil
	}
	return &Decision{Routed: true, Owner: b.Owner, Reason: b.Reason}
}

// Owners returns a sorted snapshot of (binary, owner) pairs for
// diagnostic display (`codefly policy show` style commands).
func (r *CanonicalRegistry) Owners() []OwnerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]OwnerEntry, 0, len(r.bindings))
	for b, v := range r.bindings {
		out = append(out, OwnerEntry{Binary: b, Owner: v.Owner, Reason: v.Reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Binary < out[j].Binary })
	return out
}

// OwnerEntry is one row in a registry snapshot.
type OwnerEntry struct {
	Binary string
	Owner  string
	Reason string
}

// leafName extracts the program name from a path. Mirrors the
// classic basename(1) without splitting on shell metacharacters —
// the bash parser will have done that work upstream.
func leafName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
