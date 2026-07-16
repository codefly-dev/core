package policy

import (
	"fmt"
	"math"
	"strings"
)

// Trusted session-scope environment. The manager/session host sets these after
// caller-provided environment so a plugin cannot be launched under a different
// tenant or deployment scope than the token verifier expects.
const (
	EnvToolboxTenantID    = "CODEFLY_TOOLBOX_TENANT_ID"
	EnvToolboxEnvironment = "CODEFLY_TOOLBOX_ENVIRONMENT"
	EnvToolboxReleaseID   = "CODEFLY_TOOLBOX_RELEASE_ID"
	EnvToolboxApprovalID  = "CODEFLY_TOOLBOX_APPROVAL_ID"
)

// CaveatKey is a stable, machine-readable scoped-authorization caveat name.
// Provider-specific extensions remain plain string keys and are accepted only
// when the verifier registers them explicitly.
type CaveatKey string

const (
	CaveatOrganizationID  CaveatKey = "organization_id"
	CaveatTenantID        CaveatKey = "tenant_id"
	CaveatEnvironment     CaveatKey = "environment"
	CaveatResourceBinding CaveatKey = "resource_binding"
	CaveatQueryIDs        CaveatKey = "query_ids"
	CaveatResultBudget    CaveatKey = "result_budget"
	CaveatReleaseID       CaveatKey = "release_id"
	CaveatApprovalID      CaveatKey = "approval_id"
)

// ResultBudget is the maximum result work authorized by a token. Every bound is
// required and positive so zero never ambiguously means either "none" or
// "unlimited" at a provider boundary.
type ResultBudget struct {
	MaxRows           int64 `json:"max_rows" yaml:"max_rows"`
	MaxBytes          int64 `json:"max_bytes" yaml:"max_bytes"`
	MaxDurationMillis int64 `json:"max_duration_ms" yaml:"max_duration_ms"`
}

func (b ResultBudget) Validate() error {
	if b.MaxRows <= 0 {
		return fmt.Errorf("result_budget.max_rows must be > 0")
	}
	if b.MaxBytes <= 0 {
		return fmt.Errorf("result_budget.max_bytes must be > 0")
	}
	if b.MaxDurationMillis <= 0 {
		return fmt.Errorf("result_budget.max_duration_ms must be > 0")
	}
	return nil
}

// WellKnownCaveats is the typed mint-side representation. Empty scalar fields
// are omitted. QueryIDs and ResultBudget are present only when non-empty/non-nil.
type WellKnownCaveats struct {
	OrganizationID  string
	TenantID        string
	Environment     string
	ResourceBinding string
	QueryIDs        []string
	ResultBudget    *ResultBudget
	ReleaseID       string
	ApprovalID      string
}

// Map validates and projects well-known caveats into the generic token map.
// The generic surface remains available for explicitly registered provider
// extensions; this helper removes stringly-typed drift from Codefly-owned keys.
func (c WellKnownCaveats) Map() (map[string]any, error) {
	out := make(map[string]any)
	stringsByKey := []struct {
		key   CaveatKey
		value string
	}{
		{CaveatOrganizationID, c.OrganizationID},
		{CaveatTenantID, c.TenantID},
		{CaveatEnvironment, c.Environment},
		{CaveatResourceBinding, c.ResourceBinding},
		{CaveatReleaseID, c.ReleaseID},
		{CaveatApprovalID, c.ApprovalID},
	}
	for _, item := range stringsByKey {
		if item.value == "" {
			continue
		}
		if strings.TrimSpace(item.value) != item.value {
			return nil, fmt.Errorf("%s must not have surrounding whitespace", item.key)
		}
		out[string(item.key)] = item.value
	}
	if len(c.QueryIDs) > 0 {
		seen := make(map[string]struct{}, len(c.QueryIDs))
		queryIDs := make([]string, 0, len(c.QueryIDs))
		for i, queryID := range c.QueryIDs {
			if queryID == "" || strings.TrimSpace(queryID) != queryID {
				return nil, fmt.Errorf("query_ids[%d] must be non-empty without surrounding whitespace", i)
			}
			if _, duplicate := seen[queryID]; duplicate {
				return nil, fmt.Errorf("query_ids[%d] duplicates %q", i, queryID)
			}
			seen[queryID] = struct{}{}
			queryIDs = append(queryIDs, queryID)
		}
		out[string(CaveatQueryIDs)] = queryIDs
	}
	if c.ResultBudget != nil {
		if err := c.ResultBudget.Validate(); err != nil {
			return nil, err
		}
		out[string(CaveatResultBudget)] = map[string]any{
			"max_rows":        c.ResultBudget.MaxRows,
			"max_bytes":       c.ResultBudget.MaxBytes,
			"max_duration_ms": c.ResultBudget.MaxDurationMillis,
		}
	}
	return out, nil
}

// WellKnownCaveatExpectations is the call-side binding. Every populated field
// becomes both a verifier and a required caveat; omission from the token denies.
// QueryID is the one named query being invoked. ResultBudget is the concrete
// requested budget and must fit within the token's authorized maxima.
type WellKnownCaveatExpectations struct {
	OrganizationID  string
	TenantID        string
	Environment     string
	ResourceBinding string
	QueryID         string
	ResultBudget    *ResultBudget
	ReleaseID       string
	ApprovalID      string
}

// CaveatVerification is ready to copy into VerifyExpectations.
type CaveatVerification struct {
	Verifiers map[string]CaveatVerifier
	Required  []string
}

// NewWellKnownCaveatVerification builds fail-closed verifiers for populated
// expectations. Known caveats that are not expected remain unregistered and are
// therefore rejected by the generic unknown-caveat rule.
func NewWellKnownCaveatVerification(expect WellKnownCaveatExpectations) (CaveatVerification, error) {
	verification := CaveatVerification{Verifiers: make(map[string]CaveatVerifier)}
	addExact := func(key CaveatKey, expected string) error {
		if expected == "" {
			return nil
		}
		if strings.TrimSpace(expected) != expected {
			return fmt.Errorf("%s expectation must not have surrounding whitespace", key)
		}
		name := string(key)
		verification.Required = append(verification.Required, name)
		verification.Verifiers[name] = func(value any) error {
			actual, ok := value.(string)
			if !ok {
				return fmt.Errorf("expected string, got %T", value)
			}
			if actual != expected {
				return fmt.Errorf("binding mismatch (token=%q, want=%q)", actual, expected)
			}
			return nil
		}
		return nil
	}
	for _, binding := range []struct {
		key   CaveatKey
		value string
	}{
		{CaveatOrganizationID, expect.OrganizationID},
		{CaveatTenantID, expect.TenantID},
		{CaveatEnvironment, expect.Environment},
		{CaveatResourceBinding, expect.ResourceBinding},
		{CaveatReleaseID, expect.ReleaseID},
		{CaveatApprovalID, expect.ApprovalID},
	} {
		if err := addExact(binding.key, binding.value); err != nil {
			return CaveatVerification{}, err
		}
	}
	if expect.QueryID != "" {
		if strings.TrimSpace(expect.QueryID) != expect.QueryID {
			return CaveatVerification{}, fmt.Errorf("query_id expectation must not have surrounding whitespace")
		}
		name := string(CaveatQueryIDs)
		verification.Required = append(verification.Required, name)
		verification.Verifiers[name] = func(value any) error {
			values, err := stringList(value)
			if err != nil {
				return err
			}
			for _, queryID := range values {
				if queryID == expect.QueryID {
					return nil
				}
			}
			return fmt.Errorf("query %q is not authorized", expect.QueryID)
		}
	}
	if expect.ResultBudget != nil {
		if err := expect.ResultBudget.Validate(); err != nil {
			return CaveatVerification{}, fmt.Errorf("requested %w", err)
		}
		name := string(CaveatResultBudget)
		verification.Required = append(verification.Required, name)
		verification.Verifiers[name] = func(value any) error {
			authorized, err := ParseResultBudget(value)
			if err != nil {
				return err
			}
			if expect.ResultBudget.MaxRows > authorized.MaxRows ||
				expect.ResultBudget.MaxBytes > authorized.MaxBytes ||
				expect.ResultBudget.MaxDurationMillis > authorized.MaxDurationMillis {
				return fmt.Errorf("requested result budget exceeds token maximum")
			}
			return nil
		}
	}
	return verification, nil
}

func stringList(value any) ([]string, error) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		out := make([]string, 0, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("entry %d must be a non-empty string", i)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected string list, got %T", value)
	}
}

// ParseResultBudget decodes the generic JSON/protobuf projection used in tool
// arguments and scoped caveats into its exact typed representation.
func ParseResultBudget(value any) (ResultBudget, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return ResultBudget{}, fmt.Errorf("expected object, got %T", value)
	}
	rows, err := exactInt64(fields["max_rows"])
	if err != nil {
		return ResultBudget{}, fmt.Errorf("max_rows: %w", err)
	}
	bytes, err := exactInt64(fields["max_bytes"])
	if err != nil {
		return ResultBudget{}, fmt.Errorf("max_bytes: %w", err)
	}
	duration, err := exactInt64(fields["max_duration_ms"])
	if err != nil {
		return ResultBudget{}, fmt.Errorf("max_duration_ms: %w", err)
	}
	budget := ResultBudget{MaxRows: rows, MaxBytes: bytes, MaxDurationMillis: duration}
	if err := budget.Validate(); err != nil {
		return ResultBudget{}, err
	}
	return budget, nil
}

func exactInt64(value any) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number > math.MaxInt64 || number < math.MinInt64 {
			return 0, fmt.Errorf("expected exact integer, got %v", number)
		}
		return int64(number), nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", value)
	}
}
