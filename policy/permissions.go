package policy

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/codefly-dev/core/runners/sandbox"
)

// NetworkPolicy mirrors sandbox.NetworkPolicy but is the YAML-facing
// type. Translation is one-way at policy.Apply time.
type NetworkPolicy string

const (
	// NetworkDeny severs all network access. Default for new
	// manifests; explicit zero value avoids "what does empty mean?"
	// ambiguity.
	NetworkDeny NetworkPolicy = "deny"

	// NetworkOpen leaves network unrestricted. Explicit opt-in only.
	// Auditors should grep for `network: open` in manifests.
	NetworkOpen NetworkPolicy = "open"

	// NetworkLoopback allows 127.0.0.1 only — required for the
	// agent loader's gRPC handshake to reach the plugin's
	// loopback listener, while denying every external connection.
	// Recommended secure default for plugin manifests.
	NetworkLoopback NetworkPolicy = "loopback"
)

// SandboxPolicy is the YAML-shaped permission block a plugin manifest
// declares. Example:
//
//	sandbox:
//	  read_paths:
//	    - "${WORKSPACE}"
//	  write_paths:
//	    - "${WORKSPACE}"
//	    - "${TMPDIR}"
//	  network: deny
//	  unix_sockets:
//	    - "/var/run/docker.sock"  # if this plugin needs docker access
//
// Path strings may use ${WORKSPACE}, ${TMPDIR}, ${HOME} placeholders
// that are expanded at Apply time. Absolute paths pass through.
type SandboxPolicy struct {
	ReadPaths   []string      `yaml:"read_paths,omitempty" json:"read_paths,omitempty"`
	WritePaths  []string      `yaml:"write_paths,omitempty" json:"write_paths,omitempty"`
	Network     NetworkPolicy `yaml:"network,omitempty" json:"network,omitempty"`
	UnixSockets []string      `yaml:"unix_sockets,omitempty" json:"unix_sockets,omitempty"`
}

// IsEmpty reports whether the manifest declares no capacity boundary at all.
// Local/test compatibility launch may accept this explicitly; production
// admission must reject it because an omitted policy otherwise inherits the
// host process's ambient filesystem and network authority.
func (p SandboxPolicy) IsEmpty() bool {
	return len(p.ReadPaths) == 0 &&
		len(p.WritePaths) == 0 &&
		p.Network == "" &&
		len(p.UnixSockets) == 0
}

// Validate checks the policy is internally consistent. Empty policies
// are allowed here because this is structural validation. Admission policy
// decides whether an empty declaration is acceptable for the target runtime.
func (p *SandboxPolicy) Validate() error {
	if slices.Contains(p.ReadPaths, "") {
		return fmt.Errorf("sandbox.read_paths contains an empty entry")
	}
	if slices.Contains(p.WritePaths, "") {
		return fmt.Errorf("sandbox.write_paths contains an empty entry")
	}
	if slices.Contains(p.UnixSockets, "") {
		return fmt.Errorf("sandbox.unix_sockets contains an empty entry")
	}
	switch p.Network {
	case "", NetworkDeny, NetworkOpen, NetworkLoopback:
		// "" defaults to NetworkDeny at Apply time.
	default:
		return fmt.Errorf("sandbox.network: %q is not one of {deny, open, loopback}", p.Network)
	}
	return nil
}

// PathExpander resolves placeholders in path strings: ${WORKSPACE},
// ${TMPDIR}, ${HOME}. Implementations are caller-provided so the
// policy package doesn't need to know how to find the workspace.
type PathExpander interface {
	Expand(s string) (string, error)
}

// MapExpander is the standard PathExpander backed by an explicit
// map of placeholder name (without the ${}) → expansion. Use
// NewExpander to construct one with sensible defaults seeded from
// the current process (HOME, TMPDIR) plus a caller-supplied
// WORKSPACE.
//
// Lookups for placeholders not in the map fail loud — that's the
// contract: a typo in a manifest must surface at load time, never
// silently expand to "".
type MapExpander map[string]string

// NewExpander returns a MapExpander seeded with HOME (from
// os.UserHomeDir, falling back to $HOME), TMPDIR (from os.TempDir),
// and WORKSPACE (from the supplied argument). Empty workspace
// means "no ${WORKSPACE} expansion available" — manifests using it
// will fail loudly. Callers that have additional placeholders
// (CACHE_DIR, AGENT_ROOT, …) can copy the map and add to it.
func NewExpander(workspace string) MapExpander {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	e := MapExpander{
		"HOME":   home,
		"TMPDIR": os.TempDir(),
	}
	if workspace != "" {
		e["WORKSPACE"] = workspace
	}
	return e
}

// Expand replaces ${KEY} tokens with their mapped expansions.
// Strings without "${" pass through unchanged. Unknown placeholders
// return an error naming the offending input.
func (e MapExpander) Expand(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	out := s
	for k, v := range e {
		token := "${" + k + "}"
		if strings.Contains(out, token) {
			if v == "" {
				return "", fmt.Errorf("placeholder %s has empty expansion in: %s", token, s)
			}
			out = strings.ReplaceAll(out, token, v)
		}
	}
	if strings.Contains(out, "${") {
		return "", fmt.Errorf("unknown placeholder in: %s", s)
	}
	return out, nil
}

// Apply translates a SandboxPolicy into a configured sandbox.Sandbox.
// The resulting sandbox is ready to Wrap() commands that should run
// under the policy.
//
// **Mutation contract.** Apply MUTATES sb in-place — it calls the
// fluent With* setters on the passed-in sandbox. Callers who Apply a
// policy and then call sb.WithNetwork(NetworkOpen) afterward will
// override the manifest's intent silently. Recommended pattern:
//
//	sb, _ := sandbox.New()
//	policy.Apply(&pol, sb, expand)   // configure
//	cmd := exec.Command("...")
//	sb.Wrap(cmd)                     // use; no further With* calls
//
// Don't share a sandbox across goroutines after Apply unless every
// goroutine treats it as immutable.
//
// expand is called on every path entry; it should fail loudly when a
// referenced placeholder is unset rather than silently substituting
// "" (which would create an unintended catch-all subpath rule).
//
// Paths are expanded then handed to the sandbox in single batched
// calls per category — the underlying With* methods are variadic
// and storing many slices' worth of one-element appends is wasteful.
func (p *SandboxPolicy) Apply(sb sandbox.Sandbox, expand PathExpander) error {
	readPaths, err := expandAll(expand, p.ReadPaths, "read_path")
	if err != nil {
		return err
	}
	writePaths, err := expandAll(expand, p.WritePaths, "write_path")
	if err != nil {
		return err
	}
	sockets, err := expandAll(expand, p.UnixSockets, "unix_socket")
	if err != nil {
		return err
	}

	if len(readPaths) > 0 {
		sb.WithReadPaths(readPaths...)
	}
	if len(writePaths) > 0 {
		sb.WithWritePaths(writePaths...)
	}
	if len(sockets) > 0 {
		sb.WithUnixSockets(sockets...)
	}

	// Default to deny when unset. Explicit values are honored.
	switch p.Network {
	case "", NetworkDeny:
		sb.WithNetwork(sandbox.NetworkDeny)
	case NetworkOpen:
		sb.WithNetwork(sandbox.NetworkOpen)
	case NetworkLoopback:
		sb.WithNetwork(sandbox.NetworkLoopback)
	}
	return nil
}

// =====================================================================
// PermissionPolicy — the authority/manifest layer (M4)
// =====================================================================
//
// SandboxPolicy (above) is the CAPACITY layer: what bytes/syscalls
// the binary CAN touch at the kernel level. PermissionPolicy is the
// AUTHORITY layer: what business actions the principal IS ALLOWED
// to perform, expressed as the maximum the plugin manifest claims.
//
// The two are independent and both enforced. A plugin with
// permission=[github.read_pr] running under a sandbox that denies
// network gets denied at the OS layer (capacity); a plugin running
// under network=open but with permission=[github.read_pr] gets
// denied at the PDP if it tries github.merge_pr (authority).
//
// **Why declare permissions in the manifest at all?** Because the
// manifest is the security review boundary. When a user installs a
// plugin, they review the declared permissions ONCE and the plugin
// can never silently exceed them. Even if a role grants the plugin
// admin:* (mistake or compromise), the PDP intersects role grants
// with manifest declarations — the manifest is the ceiling.
//
// **Why not enforce permissions in the plugin code?** Two reasons:
//
//  1. Plugins shouldn't make security decisions. Plugin code is
//     authored by humans across many teams; centralizing authz in
//     the host means one place to review, one place to fix.
//  2. The plugin doesn't have the role-assignment data anyway.
//     Asking "what role does this principal have?" requires hitting
//     saas-starter, which the plugin can't (and shouldn't) do.
//
// The plugin DECLARES (manifest); the host ENFORCES (PDP). Plugins
// can READ the principal context (PrincipalFrom) for audit
// branching or display, but never gate behavior on it.

// PermissionDeclaration is one entry in a plugin's permissions
// manifest block. Mirrors saas-starter's role_permissions row
// shape, with a `Reason` field that surfaces in the install-time
// review UI ("this plugin wants Action X to do Y").
//
// Strings are matched as glob patterns at PDP time:
//   - "*" matches anything
//   - "repo:codefly-dev/*" matches any repo path under that org
//   - "github.merge_pr" matches exactly that action
type PermissionDeclaration struct {
	// Action is the canonical dotted name. Examples:
	//   "github.read_pr", "fs.write", "deploy.staging".
	// May contain "*" for wildcards. Empty Action is invalid.
	Action string `yaml:"action" json:"action"`

	// Resource is the typed resource pattern.
	// Examples: "repo:${ORG}/*", "env:staging", "file:/tmp/*".
	// May contain "*" wildcards. Placeholders (${ORG}, ${WORKSPACE})
	// are expanded at install time, not at PDP-call time.
	// Empty Resource means "any resource of any type".
	Resource string `yaml:"resource" json:"resource"`

	// Reason is a human-readable explanation surfaced in the
	// install-time review. The user sees this when granting or
	// rejecting the plugin's permissions; an empty Reason makes
	// the manifest LESS reviewable, so we require it for
	// "required" entries (see PermissionPolicy.Validate).
	Reason string `yaml:"reason" json:"reason"`
}

// String produces the canonical "action on resource (reason)"
// representation used in audit logs and review prompts.
func (d PermissionDeclaration) String() string {
	if d.Resource == "" {
		return fmt.Sprintf("%s (any resource)", d.Action)
	}
	return fmt.Sprintf("%s on %s", d.Action, d.Resource)
}

// PermissionPolicy is the YAML-shaped authority block a plugin
// manifest declares. Example:
//
//	permissions:
//	  required:
//	    - action: github.read_pr
//	      resource: "repo:${ORG}/*"
//	      reason: "Inspect PRs to decide auto-merge eligibility"
//	    - action: github.merge_pr
//	      resource: "repo:${ORG}/*"
//	      reason: "Auto-merge approved PRs with green CI"
//	  optional:
//	    - action: github.deploy_staging
//	      resource: "env:staging"
//	      reason: "Trigger staging deploy after merge"
//	  risk_levels:
//	    github.merge_pr: medium
//	    github.force_push: critical
//
// **required vs optional:**
//
//   - Required entries MUST be granted at install. Without them,
//     the plugin can't function — refusing to install fails fast
//     before any tool call.
//   - Optional entries CAN be granted, but the plugin advertises
//     graceful degradation when they're not. The plugin must
//     still handle "permission denied" responses for optional
//     actions without crashing.
//
// **risk_levels** annotate actions with a risk tier (low/medium/
// high/critical). At PDP time, high-risk actions can require
// approval (M7 escalation flow) even when the role grants them.
// Manifest is the source of truth for risk classification — the
// plugin author knows the impact of their actions best.
type PermissionPolicy struct {
	// Required permissions MUST be granted at install. If any
	// required entry has no matching role grant, the install
	// fails. Plugin code can assume required permissions are
	// available.
	Required []PermissionDeclaration `yaml:"required,omitempty" json:"required,omitempty"`

	// Optional permissions enhance functionality. Plugin code
	// must handle their absence gracefully — a denied optional
	// permission must not crash; it must skip the dependent
	// behavior or fall back.
	Optional []PermissionDeclaration `yaml:"optional,omitempty" json:"optional,omitempty"`

	// RiskLevels maps action name → risk tier. Used by the PDP
	// to decide whether an action with role-grant should also
	// require human approval (M7+ flow). Valid values: "low",
	// "medium", "high", "critical". Missing entries default to
	// "low".
	RiskLevels map[string]string `yaml:"risk_levels,omitempty" json:"risk_levels,omitempty"`
}

// Risk-level constants. Use these instead of string literals so
// rename refactors catch usage at compile time.
const (
	RiskLevelLow      = "low"
	RiskLevelMedium   = "medium"
	RiskLevelHigh     = "high"
	RiskLevelCritical = "critical"
)

// IsEmpty reports whether the policy declares zero permissions.
// Used by the PDP to decide between "no manifest ceiling" (empty
// policy → no ceiling check; legacy behavior during M4 rollout)
// and "explicit empty manifest" (declared no permissions → deny
// every action). The distinction lives in the wrapping Toolbox
// resource, not here — this method is just a convenience.
func (p PermissionPolicy) IsEmpty() bool {
	return len(p.Required) == 0 && len(p.Optional) == 0
}

// All returns Required ∪ Optional in install order. The PDP uses
// this to compute the ceiling — any action not present in All() is
// outside the manifest's claimed authority and gets denied.
func (p PermissionPolicy) All() []PermissionDeclaration {
	out := make([]PermissionDeclaration, 0, len(p.Required)+len(p.Optional))
	out = append(out, p.Required...)
	out = append(out, p.Optional...)
	return out
}

// Validate checks structural invariants. Empty policies are
// allowed; the wrapping resource (Toolbox) decides what an empty
// policy means. The validation is on individual entries.
func (p *PermissionPolicy) Validate() error {
	for i, d := range p.Required {
		if err := validateDeclaration(d, true); err != nil {
			return fmt.Errorf("permissions.required[%d] (%s): %w", i, d.Action, err)
		}
	}
	for i, d := range p.Optional {
		if err := validateDeclaration(d, false); err != nil {
			return fmt.Errorf("permissions.optional[%d] (%s): %w", i, d.Action, err)
		}
	}
	for action, level := range p.RiskLevels {
		switch level {
		case RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
			// ok
		default:
			return fmt.Errorf("permissions.risk_levels[%q]: %q must be low|medium|high|critical", action, level)
		}
	}
	return nil
}

func validateDeclaration(d PermissionDeclaration, requireReason bool) error {
	if d.Action == "" {
		return fmt.Errorf("action must not be empty")
	}
	if requireReason && strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("required permissions must have a non-empty reason " +
			"(it's surfaced to the user at install time)")
	}
	if err := validateGlobPattern("action", d.Action); err != nil {
		return err
	}
	if err := validateGlobPattern("resource", d.Resource); err != nil {
		return err
	}
	return nil
}

// validateGlobPattern rejects glob forms globMatch silently
// can't match. Suffix-* is supported ("prefix*"); leading-*
// or mid-string-* parse cleanly here but never match anything
// at runtime, so authors get a confusing silent no-match.
// Fail loud at validation time instead.
func validateGlobPattern(field, pattern string) error {
	if pattern == "" || pattern == "*" {
		return nil
	}
	if !strings.Contains(pattern, "*") {
		return nil
	}
	// At this point pattern contains at least one '*' and isn't
	// the bare wildcard. Only the suffix form is supported.
	if strings.Count(pattern, "*") > 1 || !strings.HasSuffix(pattern, "*") {
		return fmt.Errorf("%s pattern %q: only exact strings, %q, or %q forms are supported",
			field, pattern, "*", "prefix*")
	}
	return nil
}

// Allows reports whether the policy DECLARES authority for the
// given (action, resource). Used by the PDP as the manifest
// ceiling check — even if a role grants the action, a manifest
// that doesn't Allow it gets denied.
//
// Matching:
//   - Action: exact match OR a declared "*" wildcard OR a glob
//     where the declared pattern ends with "*" (prefix match).
//   - Resource: exact match OR declared empty (means "any
//     resource") OR declared "*" OR a glob.
//
// **Note**: this is the manifest-ceiling check, NOT the
// role-grant check. The PDP also runs role-grant checks against
// saas-starter; both must pass.
func (p PermissionPolicy) Allows(action, resource string) bool {
	for _, d := range p.All() {
		if !globMatch(d.Action, action) {
			continue
		}
		if d.Resource == "" || globMatch(d.Resource, resource) {
			return true
		}
	}
	return false
}

// DeclaresAction reports whether at least one permission declaration covers
// action, independent of its resource constraint. Hosts use this during catalog
// admission: every advertised tool must have an install-time authority entry,
// while the concrete resource match remains a per-invocation decision.
func (p PermissionPolicy) DeclaresAction(action string) bool {
	for _, d := range p.All() {
		if globMatch(d.Action, action) {
			return true
		}
	}
	return false
}

// RiskLevelOf returns the risk tier for an action, defaulting to
// low when the manifest doesn't classify it.
func (p PermissionPolicy) RiskLevelOf(action string) string {
	if level, ok := p.RiskLevels[action]; ok && level != "" {
		return level
	}
	return RiskLevelLow
}

// globMatch implements the simple wildcard semantics for
// PermissionDeclaration matching. Supported forms:
//   - "*" matches anything
//   - "prefix*" matches anything starting with prefix
//   - exact string matches itself
//
// More complex globs (multi-segment, **) deliberately not
// supported — the manifest is meant to be readable, not a
// full pattern language. Add explicit declarations rather
// than wildcards that hide intent.
func globMatch(pattern, s string) bool {
	if pattern == "*" || pattern == s {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(s, prefix)
	}
	return false
}

// expandAll runs expand over each input, collecting expanded paths
// in order. Wraps the expander error with the YAML field name so
// the caller knows which list was involved.
func expandAll(expand PathExpander, raws []string, kind string) ([]string, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raws))
	for _, raw := range raws {
		expanded, err := expand.Expand(raw)
		if err != nil {
			return nil, fmt.Errorf("expand %s %q: %w", kind, raw, err)
		}
		out = append(out, expanded)
	}
	return out, nil
}
