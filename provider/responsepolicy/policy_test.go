package responsepolicy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
	"github.com/stretchr/testify/require"
)

// memorySink is an in-memory capture sink for tests. It records every secret
// it stored so tests can prove the raw bytes went only to the sink.
type memorySink struct {
	stored []string
	fail   bool
}

func (s *memorySink) Put(_ context.Context, target responsepolicy.SinkTarget, secret string) (*providerv0.OpaqueReference, error) {
	if s.fail {
		return nil, fmt.Errorf("sink offline")
	}
	s.stored = append(s.stored, secret)
	return &providerv0.OpaqueReference{
		Reference: fmt.Sprintf("capture://%s/%d", target.Key, len(s.stored)),
		Purpose:   target.Purpose,
	}, nil
}

func sel(path string) manifest.Selector {
	return manifest.Selector{Version: manifest.SelectorVersionV1, Path: path}
}

func forward(path string) responsepolicy.Field {
	return responsepolicy.Field{Selector: sel(path), Disposition: manifest.ResponseForwardSafe}
}

func capture(path string, required bool) responsepolicy.Field {
	return responsepolicy.Field{
		Selector:    sel(path),
		Disposition: manifest.ResponseCaptureToSink,
		Purpose:     providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		Required:    required,
		SinkKey:     "binding/secret",
	}
}

func policy(fields ...responsepolicy.Field) responsepolicy.Policy {
	return responsepolicy.Policy{Fields: fields, Limits: responsepolicy.DefaultLimits()}
}

const stripeSecret = "sk_live_1234567890abcdef"

// assertNoSecret scans arbitrary text for a planted poison value.
func assertNoSecret(t *testing.T, haystack []byte, needle string) {
	t.Helper()
	require.False(t, bytes.Contains(haystack, []byte(needle)), "poison secret leaked into %q", string(haystack))
}

func TestFilter_StripeCreateSecretCaptured(t *testing.T) {
	body := fmt.Sprintf(`{"id":"pi_123","object":"payment_intent","client_secret":%q}`, stripeSecret)
	sink := &memorySink{}
	pol := policy(forward("$.id"), forward("$.object"), capture("$.client_secret", true))

	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.NoError(t, err)

	require.Len(t, result.Captures, 1)
	require.Equal(t, responsepolicy.OutcomeCaptured, result.Captures[0].Outcome)
	require.Equal(t, []string{stripeSecret}, sink.stored)
	assertNoSecret(t, result.SafeJSON, stripeSecret)
	for _, f := range result.Forwarded {
		assertNoSecret(t, []byte(f.Value.GetStringValue()), stripeSecret)
	}
}

func TestFilter_ResendListSecretsInArray(t *testing.T) {
	body := `{"data":[{"id":"wh_1","secret":"whsec_aaa"},{"id":"wh_2","secret":"whsec_bbb"}]}`
	sink := &memorySink{}
	pol := policy(forward("$.data[*].id"), capture("$.data[*].secret", false))

	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.NoError(t, err)

	require.Len(t, result.Captures, 2)
	require.ElementsMatch(t, []string{"whsec_aaa", "whsec_bbb"}, sink.stored)
	assertNoSecret(t, result.SafeJSON, "whsec_aaa")
	assertNoSecret(t, result.SafeJSON, "whsec_bbb")
	// Both forwarded ids get distinct concrete selectors.
	require.Len(t, result.Forwarded, 2)
	require.NotEqual(t, result.Forwarded[0].Selector, result.Forwarded[1].Selector)
}

func TestFilter_SentryClientKeysBesidePublicDSN(t *testing.T) {
	body := `{"data":[{"public":"pub_1","secret":"srt_secret_1","dsn":{"public":"https://pub@sentry.io/1","secret":"https://pub:srt_secret_2@sentry.io/1"}}]}`
	sink := &memorySink{}
	pol := policy(
		forward("$.data[*].public"),
		forward("$.data[*].dsn.public"),
		capture("$.data[*].secret", true),
		capture("$.data[*].dsn.secret", true),
	)
	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.NoError(t, err)
	require.Len(t, result.Captures, 2)
	require.ElementsMatch(t, []string{"srt_secret_1", "https://pub:srt_secret_2@sentry.io/1"}, sink.stored)
	assertNoSecret(t, result.SafeJSON, "srt_secret_1")
	assertNoSecret(t, result.SafeJSON, "srt_secret_2")
}

func TestFilter_RequiredCaptureMissingFailsClosed(t *testing.T) {
	body := `{"id":"pi_123"}` // client_secret moved/renamed away
	sink := &memorySink{}
	pol := policy(forward("$.id"), capture("$.client_secret", true))
	_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.Error(t, err)
	require.Empty(t, sink.stored)
}

func TestFilter_OptionalCaptureAbsent(t *testing.T) {
	body := `{"id":"pi_123"}`
	sink := &memorySink{}
	pol := policy(forward("$.id"), capture("$.client_secret", false))
	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.NoError(t, err)
	require.Len(t, result.Captures, 1)
	require.Equal(t, responsepolicy.OutcomeAbsent, result.Captures[0].Outcome)
}

func TestFilter_CaptureWrongTypeFailsClosed(t *testing.T) {
	for _, body := range []string{
		`{"client_secret":null}`,
		`{"client_secret":123}`,
		`{"client_secret":{"nested":"x"}}`,
	} {
		sink := &memorySink{}
		pol := policy(capture("$.client_secret", true))
		_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
		require.Error(t, err, body)
		require.Empty(t, sink.stored)
	}
}

func TestFilter_DuplicateJSONKeyRejected(t *testing.T) {
	body := `{"id":"a","id":"b"}`
	pol := policy(forward("$.id"))
	_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.ErrorContains(t, err, "duplicate")
}

func TestFilter_UnknownFieldsDropped(t *testing.T) {
	body := fmt.Sprintf(`{"id":"pi_1","surprise":%q}`, stripeSecret)
	pol := policy(forward("$.id"))
	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.NoError(t, err)
	require.Len(t, result.Forwarded, 1)
	assertNoSecret(t, result.SafeJSON, stripeSecret)
}

func TestFilter_ForwardSecretShapedFailsClosed(t *testing.T) {
	// A field declared FORWARD_SAFE that drifts into carrying a secret must not
	// be forwarded.
	body := fmt.Sprintf(`{"token":%q}`, stripeSecret)
	require.True(t, configuration.LooksSecret(stripeSecret))
	pol := policy(responsepolicy.Field{Selector: sel("$.token"), Disposition: manifest.ResponseForwardSafe, Required: true})
	_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.Error(t, err)
}

func TestFilter_SinkFailureAfterMatchFailsClosed(t *testing.T) {
	body := fmt.Sprintf(`{"client_secret":%q}`, stripeSecret)
	sink := &memorySink{fail: true}
	pol := policy(capture("$.client_secret", true))
	_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", sink)
	require.Error(t, err)
}

func TestFilter_GzipBombRejected(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(bytes.Repeat([]byte("A"), 4<<20))
	_ = zw.Close()

	pol := responsepolicy.Policy{
		Fields: []responsepolicy.Field{forward("$.id")},
		Limits: responsepolicy.Limits{
			MaxCompressedBytes: 1 << 20, MaxDecompressedBytes: 1 << 20,
			MaxDepth: 16, MaxKeys: 100, MaxArrayLen: 100, MaxStringBytes: 1024,
		},
	}
	_, err := pol.Filter(context.Background(), buf.Bytes(), "gzip", "application/json", &memorySink{})
	require.ErrorContains(t, err, "decompressed byte budget")
}

func TestFilter_CallerControlledEncodingRejected(t *testing.T) {
	pol := policy(forward("$.id"))
	_, err := pol.Filter(context.Background(), []byte(`{"id":"a"}`), "br", "application/json", &memorySink{})
	require.ErrorContains(t, err, "content encoding")
}

func TestFilter_DeepNestingRejected(t *testing.T) {
	body := strings.Repeat(`{"a":`, 100) + `1` + strings.Repeat(`}`, 100)
	pol := responsepolicy.Policy{
		Fields: []responsepolicy.Field{forward("$.a")},
		Limits: responsepolicy.Limits{
			MaxCompressedBytes: 1 << 20, MaxDecompressedBytes: 1 << 20,
			MaxDepth: 16, MaxKeys: 1000, MaxArrayLen: 1000, MaxStringBytes: 1024,
		},
	}
	_, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.ErrorContains(t, err, "nesting budget")
}

func TestFilter_NonSuccessSecretLookingErrorNotForwarded(t *testing.T) {
	// A vendor error body carries an attacker-controlled secret-looking value in
	// an undeclared field. Only declared fields survive.
	body := fmt.Sprintf(`{"error":{"message":"bad","hint":%q}}`, stripeSecret)
	pol := policy(forward("$.error.message"))
	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.NoError(t, err)
	assertNoSecret(t, result.SafeJSON, stripeSecret)
}

func TestFilter_SuppressReportsPresenceOnly(t *testing.T) {
	body := fmt.Sprintf(`{"masked":%q}`, stripeSecret)
	pol := policy(responsepolicy.Field{Selector: sel("$.masked"), Disposition: manifest.ResponseSuppressPresence})
	result, err := pol.Filter(context.Background(), []byte(body), "", "application/json", &memorySink{})
	require.NoError(t, err)
	require.Equal(t, []string{"$.masked"}, result.Suppressed)
	require.Empty(t, result.Forwarded)
	assertNoSecret(t, result.SafeJSON, stripeSecret)
}

func TestFilter_GzipRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"id":"pi_9"}`))
	_ = zw.Close()
	pol := policy(forward("$.id"))
	result, err := pol.Filter(context.Background(), buf.Bytes(), "gzip", "application/json", &memorySink{})
	require.NoError(t, err)
	require.Len(t, result.Forwarded, 1)
}
