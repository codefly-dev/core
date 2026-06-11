package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// =====================================================================
// Built-in caveats — Phase 2 of the two-level model
// =====================================================================
//
// A caveat has two sides:
//
//   - Producer (gateway-side): runs at token-mint time. Either
//     decides "this call is allowed under this caveat" (e.g.
//     rate_limit checks current state, mint-time deny on
//     exhaustion) AND/OR snapshots a value into the token (e.g.
//     time_window snapshots the current window for plugin
//     verification).
//
//   - Verifier (plugin-side): runs at token-verify time. Checks
//     the snapshot in the token against current state (e.g.
//     "labels in token match labels in this call's args"). Some
//     caveats are mint-time-only with no verifier; some are
//     verify-time-only with a static spec.
//
// **Why this duality.** Different caveat shapes have different
// trust models. Rate limits are gateway-only state — the plugin
// can't independently check "have I been called too often". Label
// matches are plugin-checkable — the plugin sees the actual call
// args and can compare against the snapshot in the token. Both
// patterns serve security; the registry lets each caveat pick its
// own.
//
// **Caveat name registration.** Operators ship YAML referencing
// caveat names ("time_window", "rate_limit"). Both gateway and
// plugin look up the name in their registries and instantiate the
// matching producer / verifier. Drift in registration ⇒ plugin
// rejects the token (unknown caveat → deny).

// CaveatSpec is the YAML-derived configuration for a single
// caveat instance. Each registered caveat factory parses Spec
// into its concrete type (e.g. TimeWindowSpec for time_window).
//
// Using map[string]any keeps the YAML parser caveat-agnostic —
// it just collects Spec fields and hands them to the factory.
type CaveatSpec map[string]any

// CaveatProducerFactory builds a CaveatProducer + a deny-at-mint
// pre-check from a YAML spec. Called once per tool policy at
// load time.
//
// Returns:
//   - producer (optional): if non-nil, runs at mint time to
//     compute a value baked into the token's caveats map.
//   - precheck (optional): if non-nil, runs at mint time. nil
//     return = mint-allowed; non-nil error = mint-deny with
//     reason. Used for stateful caveats like rate_limit that
//     decide allow/deny at the gateway, no token value needed.
//
// At least one of (producer, precheck) must be non-nil. Factories
// that return both are valid (snapshot + state check).
type CaveatProducerFactory func(spec CaveatSpec) (producer CaveatProducer, precheck CaveatPrecheck, err error)

// CaveatPrecheck runs at mint time to decide if the caller may
// proceed. Returns nil = proceed; non-nil error = deny with
// reason. Stateful caveats (rate_limit) are the typical
// implementers.
type CaveatPrecheck func(ctx context.Context, in EvaluationInput) error

// CaveatVerifierFactory builds a CaveatVerifier from a YAML spec.
// Plugins instantiate these at startup from the same YAML the
// gateway used; the spec carries the policy parameters the
// verifier needs.
//
// For caveats whose verification depends ONLY on the token's
// snapshot (no spec needed at verify time), the factory ignores
// spec and returns a verifier that closes over the snapshot.
type CaveatVerifierFactory func(spec CaveatSpec) (CaveatVerifier, error)

// caveatRegistration ties a caveat name to its producer + verifier
// factories. The global registry holds these.
type caveatRegistration struct {
	name            string
	producerFactory CaveatProducerFactory
	verifierFactory CaveatVerifierFactory
}

// caveatRegistry holds all registered caveats. Populated by
// RegisterBuiltinCaveats at package init AND by operator code
// that registers custom caveats.
var (
	caveatRegistryMu sync.RWMutex
	caveatRegistry   = map[string]*caveatRegistration{}
)

// RegisterCaveat installs a caveat under the given name. Both
// producer and verifier factories are required (a producer-only
// caveat passes a no-op verifier; a verifier-only caveat passes a
// no-op producer).
//
// Re-registration of the same name PANICS — keeps drift between
// gateway and plugin loud at startup. Operators with custom
// caveats: pick a unique name (prefix with org/, e.g. "acme/cron").
func RegisterCaveat(name string, producer CaveatProducerFactory, verifier CaveatVerifierFactory) {
	if name == "" {
		panic("policy.RegisterCaveat: empty name")
	}
	if producer == nil && verifier == nil {
		panic("policy.RegisterCaveat: at least one of producer/verifier required")
	}
	caveatRegistryMu.Lock()
	defer caveatRegistryMu.Unlock()
	if _, dup := caveatRegistry[name]; dup {
		panic("policy.RegisterCaveat: duplicate name: " + name)
	}
	caveatRegistry[name] = &caveatRegistration{
		name:            name,
		producerFactory: producer,
		verifierFactory: verifier,
	}
}

// LookupCaveat returns the registration for name. Used by the
// YAML parser at load time. Returns nil if the caveat isn't
// registered — caller decides whether to error or skip.
func LookupCaveat(name string) (producer CaveatProducerFactory, verifier CaveatVerifierFactory, ok bool) {
	caveatRegistryMu.RLock()
	defer caveatRegistryMu.RUnlock()
	r, ok := caveatRegistry[name]
	if !ok {
		return nil, nil, false
	}
	return r.producerFactory, r.verifierFactory, true
}

// DefaultCaveatVerifiers returns the standard set of verifiers
// the plugin Guard should use to verify built-in caveats.
// Plugins call this once at startup and pass the result into
// guard.WithCaveatVerifiers.
//
// The verifiers it returns assume the matching gateway-side
// producer ran with a SPEC (it gets baked into the closure). For
// the built-ins, the spec comes from the YAML the operator
// authored. For custom caveats, operators register their own.
//
// Note: this returns a verifier per name with EMPTY spec —
// suitable for verifiers that only consult the token's snapshot.
// For verifiers that need the original spec at verify time
// (rare), build them explicitly via the factory + your spec.
func DefaultCaveatVerifiers() map[string]CaveatVerifier {
	out := map[string]CaveatVerifier{}
	caveatRegistryMu.RLock()
	defer caveatRegistryMu.RUnlock()
	for name, reg := range caveatRegistry {
		if reg.verifierFactory == nil {
			continue
		}
		verifier, err := reg.verifierFactory(CaveatSpec{})
		if err != nil {
			// A factory that can't construct a verifier from an
			// empty spec is producer-only or has spec-required
			// verification. Skip — operators with such caveats
			// register their own verifier set.
			continue
		}
		out[name] = verifier
	}
	return out
}

// =====================================================================
// time_window caveat — only allow during specified hours / days
// =====================================================================

// TimeWindowSpec configures the time_window caveat. Hours are
// 0-23 in the configured timezone; DaysOfWeek is a list of
// strings ("mon", "tue", ...) — empty means "any day".
//
// Both the producer (gateway) and verifier (plugin) check current
// time against the window. The token carries a snapshot of the
// SPEC (not the current time), so verifier re-checks against its
// own clock. This catches drift: even if the gateway's clock was
// off, the plugin's clock check has the operator-authored window.
type TimeWindowSpec struct {
	StartHour  int      `yaml:"start_hour" json:"start_hour"`
	EndHour    int      `yaml:"end_hour" json:"end_hour"`
	Timezone   string   `yaml:"timezone,omitempty" json:"timezone,omitempty"` // IANA name; default UTC
	DaysOfWeek []string `yaml:"days_of_week,omitempty" json:"days_of_week,omitempty"`
}

func init() {
	RegisterCaveat("time_window", timeWindowProducerFactory, timeWindowVerifierFactory)
	RegisterCaveat("rate_limit", rateLimitProducerFactory, rateLimitVerifierFactory)
	RegisterCaveat("allowlist", allowlistProducerFactory, allowlistVerifierFactory)
	// M7 / M8 audit caveats — audit-only, no semantic check at
	// plugin side. Saas-starter mints these into tokens to mark
	// the provenance: "this token was issued via M7 escalation"
	// (via_approval) or "via M8 pattern match" (via_pattern).
	// Plugin's verifier records the caveat in audit logs and
	// passes through.
	RegisterCaveat("via_approval", auditCaveatProducerFactory("via_approval"), auditCaveatVerifierFactory("via_approval"))
	RegisterCaveat("via_pattern", auditCaveatProducerFactory("via_pattern"), auditCaveatVerifierFactory("via_pattern"))
}

func parseTimeWindowSpec(spec CaveatSpec) (TimeWindowSpec, error) {
	var out TimeWindowSpec
	if v, ok := spec["start_hour"]; ok {
		n, err := toInt(v)
		if err != nil {
			return out, fmt.Errorf("time_window.start_hour: %w", err)
		}
		out.StartHour = n
	}
	if v, ok := spec["end_hour"]; ok {
		n, err := toInt(v)
		if err != nil {
			return out, fmt.Errorf("time_window.end_hour: %w", err)
		}
		out.EndHour = n
	}
	if v, ok := spec["timezone"].(string); ok {
		out.Timezone = v
	}
	if v, ok := spec["days_of_week"]; ok {
		days, err := toStringSlice(v)
		if err != nil {
			return out, fmt.Errorf("time_window.days_of_week: %w", err)
		}
		out.DaysOfWeek = days
	}
	if out.StartHour < 0 || out.StartHour > 23 {
		return out, fmt.Errorf("time_window.start_hour out of range [0,23]: %d", out.StartHour)
	}
	if out.EndHour < 0 || out.EndHour > 24 {
		return out, fmt.Errorf("time_window.end_hour out of range [0,24]: %d", out.EndHour)
	}
	for _, d := range out.DaysOfWeek {
		if _, err := parseWeekday(d); err != nil {
			return out, err
		}
	}
	return out, nil
}

func timeWindowProducerFactory(spec CaveatSpec) (CaveatProducer, CaveatPrecheck, error) {
	tw, err := parseTimeWindowSpec(spec)
	if err != nil {
		return nil, nil, err
	}
	loc, err := loadTimezone(tw.Timezone)
	if err != nil {
		return nil, nil, err
	}

	precheck := func(_ context.Context, _ EvaluationInput) error {
		now := time.Now().In(loc)
		if !inWindow(now, tw, loc) {
			return fmt.Errorf("time_window: now (%s) outside %02d:00-%02d:00 %s",
				now.Format("Mon 15:04 MST"), tw.StartHour, tw.EndHour, loc.String())
		}
		return nil
	}

	// Producer snapshots the spec into the token. The plugin's
	// verifier uses the snapshot to re-check current time at
	// verify (defense against gateway clock drift).
	producer := func(_ EvaluationInput) (any, error) {
		// Re-encode as map for clean JSON.
		return map[string]any{
			"start_hour":   tw.StartHour,
			"end_hour":     tw.EndHour,
			"timezone":     tw.Timezone,
			"days_of_week": tw.DaysOfWeek,
		}, nil
	}
	return producer, precheck, nil
}

func timeWindowVerifierFactory(_ CaveatSpec) (CaveatVerifier, error) {
	// The verifier reads the snapshot off the token (passed as v).
	return func(v any) error {
		spec, err := caveatValueToSpec(v)
		if err != nil {
			return fmt.Errorf("time_window: token caveat malformed: %w", err)
		}
		tw, err := parseTimeWindowSpec(spec)
		if err != nil {
			return err
		}
		loc, err := loadTimezone(tw.Timezone)
		if err != nil {
			return err
		}
		now := time.Now().In(loc)
		if !inWindow(now, tw, loc) {
			return fmt.Errorf("time_window: now (%s) outside %02d:00-%02d:00 %s",
				now.Format("Mon 15:04 MST"), tw.StartHour, tw.EndHour, loc.String())
		}
		return nil
	}, nil
}

func loadTimezone(tz string) (*time.Location, error) {
	if tz == "" || tz == "UTC" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

func inWindow(now time.Time, tw TimeWindowSpec, _ *time.Location) bool {
	if len(tw.DaysOfWeek) > 0 {
		current := strings.ToLower(now.Weekday().String()[:3])
		ok := false
		for _, d := range tw.DaysOfWeek {
			if strings.ToLower(d) == current {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	hour := now.Hour()
	// EndHour is exclusive; 9-17 means 9:00:00 - 16:59:59.
	// Both wrapping (start > end, e.g. 22-6 for overnight) and
	// non-wrapping cases supported.
	if tw.StartHour == tw.EndHour {
		// Empty window — nothing matches. Catches a misconfig
		// (forgotten end_hour stays 0).
		return false
	}
	if tw.StartHour < tw.EndHour {
		return hour >= tw.StartHour && hour < tw.EndHour
	}
	// Wrapping: start > end (e.g. 22-6).
	return hour >= tw.StartHour || hour < tw.EndHour
}

func parseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(s) {
	case "sun", "sunday":
		return time.Sunday, nil
	case "mon", "monday":
		return time.Monday, nil
	case "tue", "tuesday":
		return time.Tuesday, nil
	case "wed", "wednesday":
		return time.Wednesday, nil
	case "thu", "thursday":
		return time.Thursday, nil
	case "fri", "friday":
		return time.Friday, nil
	case "sat", "saturday":
		return time.Saturday, nil
	}
	return 0, fmt.Errorf("unknown day-of-week: %q", s)
}

// =====================================================================
// rate_limit caveat — sliding-window token bucket per (principal, tool)
// =====================================================================

// RateLimitSpec configures rate_limit. PerMinute is the maximum
// number of mint operations per (principal_id, action) tuple in
// the most recent 60-second window.
//
// **Mint-only.** rate_limit decides at the gateway and emits no
// caveat into the token. The plugin doesn't know about rate
// limits — by design, that's a gateway concern. Once the token
// is minted, rate-limit was already accepted; using it within
// the (short) TTL is fine.
type RateLimitSpec struct {
	PerMinute int    `yaml:"per_minute" json:"per_minute"`
	Scope     string `yaml:"scope,omitempty" json:"scope,omitempty"` // "principal" (default) | "global" | "principal_org"
}

func parseRateLimitSpec(spec CaveatSpec) (RateLimitSpec, error) {
	var out RateLimitSpec
	if v, ok := spec["per_minute"]; ok {
		n, err := toInt(v)
		if err != nil {
			return out, fmt.Errorf("rate_limit.per_minute: %w", err)
		}
		out.PerMinute = n
	}
	if v, ok := spec["scope"].(string); ok {
		out.Scope = v
	}
	if out.PerMinute <= 0 {
		return out, errors.New("rate_limit.per_minute must be > 0")
	}
	switch out.Scope {
	case "", "principal", "principal_org", "global":
		// ok
	default:
		return out, fmt.Errorf("rate_limit.scope: %q must be principal|principal_org|global", out.Scope)
	}
	if out.Scope == "" {
		out.Scope = "principal"
	}
	return out, nil
}

// rateLimitState is the shared in-memory state across all
// instances of the rate_limit caveat for the same scope+key. We
// keep one map per spec — different specs (different per_minute
// values) don't share state.
type rateLimitState struct {
	mu      sync.Mutex
	windows map[string][]time.Time // key → mint timestamps in last 60s
}

func newRateLimitState() *rateLimitState {
	return &rateLimitState{windows: map[string][]time.Time{}}
}

func (s *rateLimitState) check(key string, perMinute int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := now.Add(-time.Minute)
	timestamps := s.windows[key]

	// Drop any entries before the cutoff.
	pruned := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	if len(pruned) >= perMinute {
		s.windows[key] = pruned
		return fmt.Errorf("rate_limit: %d/min exceeded (%d in last 60s)", perMinute, len(pruned))
	}
	pruned = append(pruned, now)
	s.windows[key] = pruned
	return nil
}

func rateLimitKey(scope string, in EvaluationInput) string {
	switch scope {
	case "global":
		return in.Toolbox + ":" + in.Tool
	case "principal_org":
		if in.Principal != nil {
			return in.Principal.OrgID + ":" + in.Toolbox + ":" + in.Tool
		}
		return ":" + in.Toolbox + ":" + in.Tool
	case "principal", "":
		fallthrough
	default:
		if in.Principal != nil {
			return in.Principal.ID + ":" + in.Toolbox + ":" + in.Tool
		}
		return "anon:" + in.Toolbox + ":" + in.Tool
	}
}

func rateLimitProducerFactory(spec CaveatSpec) (CaveatProducer, CaveatPrecheck, error) {
	rl, err := parseRateLimitSpec(spec)
	if err != nil {
		return nil, nil, err
	}
	state := newRateLimitState()
	precheck := func(_ context.Context, in EvaluationInput) error {
		key := rateLimitKey(rl.Scope, in)
		return state.check(key, rl.PerMinute, time.Now())
	}
	// No producer — rate_limit is mint-only, no caveat travels.
	return nil, precheck, nil
}

func rateLimitVerifierFactory(_ CaveatSpec) (CaveatVerifier, error) {
	// Plugin-side: rate_limit emits no caveat, so if the verifier
	// EVER fires, something's wrong. Return an error to make
	// drift visible.
	return func(v any) error {
		return fmt.Errorf("rate_limit: token unexpectedly carries this caveat (gateway misconfigured)")
	}, nil
}

// =====================================================================
// allowlist caveat — list-membership snapshot
// =====================================================================
//
// allowlist is the generic list-membership caveat. Operators
// configure: "this caveat key in EvaluationInput.Context must be
// in this list at mint time". Plugin-side: the value is
// snapshotted into the token; verifier checks the runtime call's
// context value against the snapshot.
//
// Use cases: "ci_status must be one of [green, success]",
// "branch must match production-branch list", "label includes
// auto-merge".

type AllowlistSpec struct {
	// ContextKey is the field in EvaluationInput.Context to check.
	ContextKey string `yaml:"context_key" json:"context_key"`
	// Allowed is the set of acceptable values.
	Allowed []string `yaml:"allowed" json:"allowed"`
	// MatchMode: "equals" (default) | "any_of_list" (context value is a list, any matches)
	MatchMode string `yaml:"match_mode,omitempty" json:"match_mode,omitempty"`
}

func parseAllowlistSpec(spec CaveatSpec) (AllowlistSpec, error) {
	var out AllowlistSpec
	if v, ok := spec["context_key"].(string); ok {
		out.ContextKey = v
	}
	if v, ok := spec["allowed"]; ok {
		ss, err := toStringSlice(v)
		if err != nil {
			return out, fmt.Errorf("allowlist.allowed: %w", err)
		}
		out.Allowed = ss
	}
	if v, ok := spec["match_mode"].(string); ok {
		out.MatchMode = v
	}
	if out.ContextKey == "" {
		return out, errors.New("allowlist.context_key required")
	}
	if len(out.Allowed) == 0 {
		return out, errors.New("allowlist.allowed must be non-empty")
	}
	switch out.MatchMode {
	case "", "equals", "any_of_list":
		// ok
	default:
		return out, fmt.Errorf("allowlist.match_mode: %q must be equals|any_of_list", out.MatchMode)
	}
	if out.MatchMode == "" {
		out.MatchMode = "equals"
	}
	return out, nil
}

func allowlistMatches(value any, spec AllowlistSpec) bool {
	switch spec.MatchMode {
	case "any_of_list":
		// Value is expected to be a list; match if ANY element
		// is in Allowed.
		switch vs := value.(type) {
		case []string:
			for _, v := range vs {
				if containsString(spec.Allowed, v) {
					return true
				}
			}
		case []any:
			for _, v := range vs {
				if s, ok := v.(string); ok && containsString(spec.Allowed, s) {
					return true
				}
			}
		}
		return false
	default: // "equals"
		s, ok := value.(string)
		if !ok {
			return false
		}
		return containsString(spec.Allowed, s)
	}
}

func allowlistProducerFactory(spec CaveatSpec) (CaveatProducer, CaveatPrecheck, error) {
	al, err := parseAllowlistSpec(spec)
	if err != nil {
		return nil, nil, err
	}
	// Mint-time check: context value must match.
	precheck := func(_ context.Context, in EvaluationInput) error {
		v, ok := in.Context[al.ContextKey]
		if !ok {
			return fmt.Errorf("allowlist: context missing key %q", al.ContextKey)
		}
		if !allowlistMatches(v, al) {
			return fmt.Errorf("allowlist: %q=%v not in allowed set %v", al.ContextKey, v, al.Allowed)
		}
		return nil
	}
	// Producer snapshots both the spec AND the matched value.
	// Plugin-side verifier checks: (a) spec matches what plugin
	// expects, (b) token's snapshot value still matches current
	// call context.
	producer := func(in EvaluationInput) (any, error) {
		v := in.Context[al.ContextKey]
		return map[string]any{
			"context_key":    al.ContextKey,
			"allowed":        al.Allowed,
			"match_mode":     al.MatchMode,
			"snapshot_value": v,
		}, nil
	}
	return producer, precheck, nil
}

func allowlistVerifierFactory(_ CaveatSpec) (CaveatVerifier, error) {
	return func(v any) error {
		m, err := caveatValueToSpec(v)
		if err != nil {
			return fmt.Errorf("allowlist: token caveat malformed: %w", err)
		}
		spec, err := parseAllowlistSpec(m)
		if err != nil {
			return err
		}
		// At verify time we use the SNAPSHOT value from the
		// token. The plugin doesn't have access to the original
		// gateway-side EvaluationInput.Context, so we trust the
		// snapshot. The gateway authorized THIS call based on
		// THIS state — if state changed by call time (e.g. the
		// PR's labels changed), that's fine: the gateway said
		// yes when state was good.
		snapshot, ok := m["snapshot_value"]
		if !ok {
			return errors.New("allowlist: token missing snapshot_value")
		}
		if !allowlistMatches(snapshot, spec) {
			return fmt.Errorf("allowlist: snapshot %v not in allowed %v (gateway misminted)",
				snapshot, spec.Allowed)
		}
		return nil
	}, nil
}

// =====================================================================
// Helpers — type coercion for spec parsing
// =====================================================================

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case int32:
		return int(n), nil
	case float64:
		// YAML numerics often come through as float64 via
		// encoding/json or yaml.Unmarshal-into-any.
		return int(n), nil
	case string:
		// Tolerate string-encoded numbers from operators who
		// quote everything in YAML. fmt.Sscanf handles "5", "  5  ".
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &out); err != nil {
			return 0, fmt.Errorf("not a number: %q", n)
		}
		return out, nil
	}
	return 0, fmt.Errorf("expected int, got %T", v)
}

func toStringSlice(v any) ([]string, error) {
	switch s := v.(type) {
	case []string:
		return s, nil
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			str, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("expected string in list, got %T", e)
			}
			out = append(out, str)
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected list, got %T", v)
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// =====================================================================
// Audit caveats — via_approval, via_pattern
// =====================================================================
//
// These caveats are AUDIT-ONLY: they record provenance in the
// token but don't enforce semantic constraints. Saas-starter
// mints them on M7 (via_approval) or M8 (via_pattern) tokens to
// mark how the token came to exist. Plugin-side verifiers
// accept any value (the signature already proved authenticity)
// and rely on the existing audit-log infrastructure to record
// them.
//
// Why two separate caveats rather than one "provenance" caveat:
// distinct keys make grep filters trivial. "Show me everything
// approved via M7" is `caveats.via_approval IS NOT NULL`. With
// a single key we'd need value-based filtering.
//
// Convention for caveat values (saas-starter side):
//
//	via_approval:
//	  grant_id:      "ulid-..."        # delegation_grants.id
//	  grantor_id:    "u-..."           # who approved
//	  approved_at:   1715300000        # unix seconds
//
//	via_pattern:
//	  grant_id:      "ulid-..."        # delegation_grants.id (kind='pattern')
//	  pattern:       "github.merge_pr@auto-merge"  # the matched pattern
//	  match_count:   42                # how many times this pattern has matched

// auditCaveatProducerFactory returns a factory for an audit-only
// caveat. Producer is nil — operators emit these from outside
// (saas-starter mint code), not from a YAML-driven producer.
// Plugins consume them via verifier.
func auditCaveatProducerFactory(name string) CaveatProducerFactory {
	return func(spec CaveatSpec) (CaveatProducer, CaveatPrecheck, error) {
		// Audit caveats have no precheck or producer — they're
		// minted directly by the M7/M8 backend, not the YAML
		// pipeline. If someone tries to declare one in YAML
		// (`caveats: { via_approval: {} }`), reject loudly so
		// they don't think it does anything.
		_ = name
		return nil, nil, fmt.Errorf("%s: audit caveat is not declarable in YAML; minted directly by M7/M8 backend", name)
	}
}

// auditCaveatVerifierFactory returns a verifier that accepts any
// value. Saas-starter mints these into tokens; plugins verify
// only that the token's signature is valid (already done) and
// that the caveat is present.
func auditCaveatVerifierFactory(name string) CaveatVerifierFactory {
	return func(_ CaveatSpec) (CaveatVerifier, error) {
		return func(v any) error {
			// Accept any non-nil value. The signature check has
			// already proven the audit data wasn't tampered.
			if v == nil {
				return fmt.Errorf("%s: nil caveat value (saas-starter must populate the metadata map)", name)
			}
			return nil
		}, nil
	}
}

// caveatValueToSpec coerces a JSON-decoded caveat value back into
// CaveatSpec shape. JSON numbers become float64; the spec
// parsers tolerate that via toInt.
func caveatValueToSpec(v any) (CaveatSpec, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map, got %T", v)
	}
	return CaveatSpec(m), nil
}
