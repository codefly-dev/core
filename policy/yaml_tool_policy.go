package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// =====================================================================
// YAML tool-policy schema — operator-facing
// =====================================================================
//
// Operators ship tool policies as YAML files. ParseYAMLToolPolicies
// turns them into a map[string]ToolPolicy ready to drop into
// GatewayEvaluator.ToolPolicies.
//
// Why YAML rather than Go: operators tune policies more often than
// they recompile codefly. A YAML file lives next to the gateway's
// config, gets versioned in operations git, can be templated by
// Helm/Kustomize. Adding a new tool's policy doesn't touch any
// Go code.
//
// Why not Cedar / Rego directly: they're powerful but heavy. For
// the 80% of tool policies that just want "allow during business
// hours" or "deny without auto-merge label", YAML covers it
// without dragging in a policy engine. Operators with richer needs
// implement ToolPolicy directly OR wire CedarToolPolicy (cedar.go).
//
// Example:
//
//	# tool-policies.yaml
//	tools:
//	  - id: codefly.dev/github-bot:0.1.0:github.merge_pr
//	    allow: true
//	    ttl: 60s
//	    max_uses: 1
//	    caveats:
//	      time_window:
//	        start_hour: 9
//	        end_hour: 17
//	        timezone: "America/New_York"
//	        days_of_week: [mon, tue, wed, thu, fri]
//	      rate_limit:
//	        per_minute: 5
//	        scope: principal
//	      allowlist:
//	        context_key: ci_status
//	        allowed: [green, success]
//
//	  - id: codefly.dev/github-bot:0.1.0:github.force_push
//	    deny: true
//	    reason: "force-push requires manual approval (M7+)"
//
//	  - id: codefly.dev/github-bot:0.1.0:*
//	    allow: true
//	    ttl: 120s

// YAMLToolPolicies is the top-level YAML structure.
type YAMLToolPolicies struct {
	Tools []YAMLToolPolicy `yaml:"tools"`
}

// YAMLToolPolicy is one policy entry. Either Allow or Deny must
// be true; both true is rejected at parse.
type YAMLToolPolicy struct {
	// ID is the tool key: "<toolbox>:<tool>" or "<toolbox>:*".
	// Examples:
	//   "codefly.dev/github-bot:0.1.0:github.merge_pr"
	//   "codefly.dev/github-bot:0.1.0:*"
	ID string `yaml:"id"`

	// Allow / Deny — exactly one must be true.
	Allow bool `yaml:"allow,omitempty"`
	Deny  bool `yaml:"deny,omitempty"`

	// Reason — required for Deny, optional for Allow.
	Reason string `yaml:"reason,omitempty"`

	// TTL is the token lifetime. Parsed via time.ParseDuration.
	// Examples: "60s", "5m", "1h". Zero/empty uses
	// GatewayEvaluator.DefaultTTL.
	TTL string `yaml:"ttl,omitempty"`

	// MaxUses caps token reuse. Zero/missing → 1 (single-shot).
	MaxUses int `yaml:"max_uses,omitempty"`

	// Caveats maps caveat name → spec. Each name must be
	// registered (built-ins or operator-supplied via
	// RegisterCaveat). Unknown names fail at parse.
	Caveats map[string]CaveatSpec `yaml:"caveats,omitempty"`
}

// ParseYAMLToolPolicies parses YAML bytes into ToolPolicy
// implementations keyed by tool id, ready for GatewayEvaluator.
// All caveat references are resolved at parse time; unknown
// caveat names produce an error.
//
// The returned map keys exactly match what GatewayEvaluator's
// lookup expects: <toolbox>:<tool> or <toolbox>:*.
func ParseYAMLToolPolicies(data []byte) (map[string]ToolPolicy, error) {
	var raw YAMLToolPolicies
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	return raw.Build()
}

// Build is ParseYAMLToolPolicies's second half — turns a parsed
// YAMLToolPolicies into ToolPolicy implementations. Exported so
// callers who already have a YAMLToolPolicies in memory (e.g.
// from a config-merge pipeline) can build without re-parsing.
func (y YAMLToolPolicies) Build() (map[string]ToolPolicy, error) {
	out := make(map[string]ToolPolicy, len(y.Tools))
	seen := make(map[string]int, len(y.Tools))
	for i, raw := range y.Tools {
		if raw.ID == "" {
			return nil, fmt.Errorf("tool[%d]: id is required", i)
		}
		if prev, dup := seen[raw.ID]; dup {
			return nil, fmt.Errorf("tool[%d]: duplicate id %q (also at tool[%d])", i, raw.ID, prev)
		}
		seen[raw.ID] = i

		if raw.Allow == raw.Deny {
			return nil, fmt.Errorf("tool[%d] id=%q: exactly one of allow/deny must be true (allow=%v, deny=%v)",
				i, raw.ID, raw.Allow, raw.Deny)
		}

		policy, err := raw.toToolPolicy()
		if err != nil {
			return nil, fmt.Errorf("tool[%d] id=%q: %w", i, raw.ID, err)
		}
		out[raw.ID] = policy
	}
	return out, nil
}

func (raw YAMLToolPolicy) toToolPolicy() (ToolPolicy, error) {
	if raw.Deny {
		reason := raw.Reason
		if reason == "" {
			return nil, errors.New("deny policies must have a non-empty reason")
		}
		return DenyAlwaysToolPolicy{Reason: reason}, nil
	}

	// Allow path: parse TTL, MaxUses, caveats.
	var ttl time.Duration
	if raw.TTL != "" {
		d, err := time.ParseDuration(raw.TTL)
		if err != nil {
			return nil, fmt.Errorf("ttl: %w", err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("ttl must be > 0 (got %s)", raw.TTL)
		}
		ttl = d
	}
	maxUses := raw.MaxUses
	if maxUses < 0 {
		return nil, fmt.Errorf("max_uses must be >= 0 (got %d)", maxUses)
	}

	if len(raw.Caveats) == 0 {
		return AllowAlwaysToolPolicy{TTL: ttl, MaxUses: maxUses}, nil
	}

	// Build the caveat producers + prechecks at parse time.
	prechecks := make([]CaveatPrecheck, 0, len(raw.Caveats))
	producers := make(map[string]CaveatProducer, len(raw.Caveats))
	for name, spec := range raw.Caveats {
		producerFactory, _, ok := LookupCaveat(name)
		if !ok {
			return nil, fmt.Errorf("caveat %q is not registered (use RegisterCaveat or check spelling)", name)
		}
		if producerFactory == nil {
			return nil, fmt.Errorf("caveat %q has no producer factory", name)
		}
		producer, precheck, err := producerFactory(spec)
		if err != nil {
			return nil, fmt.Errorf("caveat %q: %w", name, err)
		}
		if precheck != nil {
			prechecks = append(prechecks, precheck)
		}
		if producer != nil {
			producers[name] = producer
		}
	}

	return &yamlToolPolicy{
		ttl:       ttl,
		maxUses:   maxUses,
		prechecks: prechecks,
		producers: producers,
	}, nil
}

// yamlToolPolicy is the concrete ToolPolicy that the YAML parser
// produces. Composes prechecks (run sequentially; first deny
// wins) with producers (snapshot into token's caveats map).
type yamlToolPolicy struct {
	ttl       time.Duration
	maxUses   int
	prechecks []CaveatPrecheck
	producers map[string]CaveatProducer
}

func (p *yamlToolPolicy) Evaluate(ctx context.Context, in EvaluationInput) (ResolvedToolPolicy, error) {
	for _, check := range p.prechecks {
		if err := check(ctx, in); err != nil {
			return ResolvedToolPolicy{}, err
		}
	}
	return ResolvedToolPolicy{
		TTL:             p.ttl,
		MaxUses:         p.maxUses,
		CaveatProducers: p.producers,
	}, nil
}
