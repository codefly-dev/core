package policy

import (
	"fmt"
	"os"
	"strings"
)

// PDPMode is the env-driven mode selector for the permission system.
//
// Operators set CODEFLY_PDP_MODE before starting codefly host. The
// chosen mode determines which PDP wrappers stack on the inner
// (saas-starter-backed) PDP at startup, controlling how decisions
// surface in production.
//
// **Why an env var rather than config file.** Mode flipping is an
// ops-time operation (switching enforce on after a shadow burn-in)
// that should be a single env-var change + restart, not a code
// release. Same pattern as CODEFLY_DEBUG, OTEL_*, etc.
type PDPMode string

const (
	// PDPModeOff disables permission enforcement entirely. NO Guard
	// is wired around the toolbox; every CallTool passes through.
	// Used for development, tests, and any deploy where saas-starter
	// isn't available.
	//
	// **Production should NEVER run with mode=off.** Operators must
	// flip to shadow first (decisions logged) before enforce.
	PDPModeOff PDPMode = "off"

	// PDPModeShadow logs every decision via observability but always
	// returns Allow. Use this DURING the M5 rollout: real PDP runs
	// against real traffic, you watch what would-have-been denied,
	// fix policy drift, then flip to enforce. Without shadow, the
	// first deny in production is also the first time anyone sees
	// what enforce mode would block — high incident risk.
	PDPModeShadow PDPMode = "shadow"

	// PDPModeEnforce is the production target. Decisions are honored
	// (deny short-circuits the call). Only flip to enforce after a
	// burn-in period in shadow mode shows zero false-deny against
	// legitimate traffic.
	PDPModeEnforce PDPMode = "enforce"
)

// EnvPDPMode is the env-var name. Centralized as a constant so a
// rename refactors callers reliably.
const EnvPDPMode = "CODEFLY_PDP_MODE"

// EnvPDPRequireManifest controls the CeilingPDP's RequireManifest
// flag. true = empty manifest denies all; false = empty manifest
// passes through (M4 rollout default). Operators flip to true once
// every plugin has been audited and declares its permissions.
const EnvPDPRequireManifest = "CODEFLY_PDP_REQUIRE_MANIFEST"

// ResolvePDPMode reads CODEFLY_PDP_MODE from the environment and
// returns the validated mode. Returns an error if the value is set
// but unrecognized — silently defaulting on a typo would be a
// security footgun ("CODEFLY_PDP_MODE=enfocre" silently → off).
//
// Empty/unset env defaults to PDPModeOff with a clear startup log
// from the host (the host should log the resolved mode loudly so
// it appears in incident-investigation logs).
func ResolvePDPMode() (PDPMode, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvPDPMode)))
	switch raw {
	case "":
		return PDPModeOff, nil
	case string(PDPModeOff), string(PDPModeShadow), string(PDPModeEnforce):
		return PDPMode(raw), nil
	default:
		return "", fmt.Errorf("policy: %s=%q is not one of {off, shadow, enforce}", EnvPDPMode, raw)
	}
}

// ResolveRequireManifest reads CODEFLY_PDP_REQUIRE_MANIFEST. Truthy
// values: "1", "true", "yes" (case-insensitive). Anything else =
// false. Empty/unset = false (the M4 rollout default).
func ResolveRequireManifest() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvPDPRequireManifest)))
	switch raw {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// BuildPDP composes the PDP stack the host installs into
// PluginRegistration.PDP. Layers (top-down):
//
//   - Mode shadow:   ShadowPDP wraps the rest (logs, always allow)
//   - Mode off:      AllowAllPDP returned (no Guard installed)
//   - Mode enforce:  no extra wrapper; raw stack enforces
//
// Inside the mode wrapper, the stack is always:
//
//   - CeilingPDP wraps inner (manifest ceiling check first)
//   - Inner is whatever the caller supplies (typically SaasPDP)
//
// **Why this composition.** Manifest ceiling SHOULD short-circuit
// before the saas-starter round-trip, so it's the innermost wrapper
// (closest to the request). Shadow wraps the OUTSIDE so it observes
// the final composed decision (including ceiling-denies) and can
// log+allow them all the same way.
//
// Caller passes `manifest` per plugin (different plugins have
// different manifests). `inner` is shared across plugins (one
// SaasPDP for the whole host).
//
// Pass nil metrics to opt out of counter recording in shadow mode.
func BuildPDP(mode PDPMode, inner PDP, manifest PermissionPolicy, requireManifest bool, metrics *PDPMetrics) PDP {
	switch mode {
	case PDPModeOff:
		// No Guard at all. Caller should NOT install a Guard at the
		// agents.Serve level — set PluginRegistration.PDP to nil
		// instead. Returning AllowAllPDP here is defensive: if the
		// caller wires us anyway, every call passes.
		return AllowAllPDP{}
	case PDPModeShadow:
		ceiling := NewCeilingPDP(inner, manifest, requireManifest)
		return NewShadowPDP(ceiling, metrics)
	case PDPModeEnforce:
		return NewCeilingPDP(inner, manifest, requireManifest)
	default:
		// Defense — should never hit. ResolvePDPMode catches typos.
		return AllowAllPDP{}
	}
}
