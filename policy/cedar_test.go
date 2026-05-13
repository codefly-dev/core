package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

// stubExternalEvaluator implements ExternalPolicyEvaluator with a
// programmable verdict — stand-in for an actual Cedar/OPA engine.
type stubExternalEvaluator struct {
	decision policy.ExternalDecision
	err      error
}

func (s stubExternalEvaluator) Evaluate(_ context.Context, _ policy.EvaluationInput) (policy.ExternalDecision, error) {
	return s.decision, s.err
}

func TestExternalToolPolicy_Allow_Passes(t *testing.T) {
	stub := stubExternalEvaluator{
		decision: policy.ExternalDecision{
			Allow:   true,
			TTL:     45 * time.Second,
			MaxUses: 2,
			Caveats: map[string]any{"engine_score": 0.93},
		},
	}
	p := &policy.ExternalToolPolicy{Evaluator: stub}

	resolved, err := p.Evaluate(context.Background(), policy.EvaluationInput{
		Tool: "x.y",
	})
	require.NoError(t, err)
	require.Equal(t, 45*time.Second, resolved.TTL)
	require.Equal(t, 2, resolved.MaxUses)
	require.NotNil(t, resolved.CaveatProducers["engine_score"])
}

func TestExternalToolPolicy_Deny_SurfacesReason(t *testing.T) {
	stub := stubExternalEvaluator{
		decision: policy.ExternalDecision{
			Allow:  false,
			Reason: "Cedar: principal not in admin set",
		},
	}
	p := &policy.ExternalToolPolicy{Evaluator: stub}

	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x.y"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Cedar: principal not in admin set")
}

func TestExternalToolPolicy_DenyEmptyReason_DefaultMessage(t *testing.T) {
	stub := stubExternalEvaluator{decision: policy.ExternalDecision{Allow: false}}
	p := &policy.ExternalToolPolicy{Evaluator: stub}

	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x.y"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "denied")
}

func TestExternalToolPolicy_EvaluatorError_PropagatesAsError(t *testing.T) {
	stub := stubExternalEvaluator{err: errors.New("Rego eval crashed")}
	p := &policy.ExternalToolPolicy{Evaluator: stub}

	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x.y"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Rego eval crashed")
}

func TestExternalToolPolicy_NilEvaluator_Errors(t *testing.T) {
	p := &policy.ExternalToolPolicy{} // no evaluator
	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x.y"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Evaluator")
}

func TestExternalToolPolicy_CaveatProducers_EmitStaticSnapshot(t *testing.T) {
	stub := stubExternalEvaluator{
		decision: policy.ExternalDecision{
			Allow:   true,
			Caveats: map[string]any{"engine_decision": "compliant"},
		},
	}
	p := &policy.ExternalToolPolicy{Evaluator: stub}
	resolved, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x"})
	require.NoError(t, err)

	producer := resolved.CaveatProducers["engine_decision"]
	require.NotNil(t, producer)
	v, err := producer(policy.EvaluationInput{})
	require.NoError(t, err)
	require.Equal(t, "compliant", v)
}
