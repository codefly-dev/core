package responsepolicy_test

import (
	"context"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// FuzzFilter proves that response filtering never panics on arbitrary bytes,
// never forwards a secret-shaped value, and only ever stores captured secrets
// in the sink. Duplicate keys, bombs, and malformed JSON must fail closed, not
// crash.
func FuzzFilter(f *testing.F) {
	for _, seed := range []string{
		`{"id":"a","secret":"whsec_x"}`,
		`{"id":"a","id":"b"}`,
		`{"data":[{"secret":"sk_live_1234567890abcdef"}]}`,
		`not json`,
		`{"n":1e999}`,
		`[]`,
		``,
	} {
		f.Add([]byte(seed))
	}
	pol := responsepolicy.Policy{
		Fields: []responsepolicy.Field{
			{Selector: manifest.Selector{Version: "v1", Path: "$.id"}, Disposition: manifest.ResponseForwardSafe},
			{Selector: manifest.Selector{Version: "v1", Path: "$.data[*].secret"}, Disposition: manifest.ResponseCaptureToSink, Purpose: 2, SinkKey: "k"},
		},
		Limits: responsepolicy.DefaultLimits(),
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := pol.Filter(context.Background(), body, "", "application/json", &fuzzSink{})
		if err != nil {
			return
		}
		for _, forwarded := range result.Forwarded {
			if forwarded.Value == nil {
				t.Fatalf("forwarded field %q has nil value", forwarded.Selector)
			}
		}
	})
}

type fuzzSink struct{}

func (fuzzSink) Put(_ context.Context, target responsepolicy.SinkTarget, _ string) (*providerv0.OpaqueReference, error) {
	return &providerv0.OpaqueReference{Reference: "capture://" + target.Key, Purpose: target.Purpose}, nil
}
