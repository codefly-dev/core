package cassette_test

import (
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/stretchr/testify/require"
)

func plannedRequest(policyDigest string) *providerv0.PlannedRequest {
	return &providerv0.PlannedRequest{
		RequestDescriptorId:  "account.create",
		Method:               providerv0.HTTPMethod_HTTP_METHOD_POST,
		ResponsePolicyDigest: policyDigest,
		IdempotencyKey:       "idem-volatile",
	}
}

func admittedOrigin() *providerv0.AdmittedOrigin {
	return &providerv0.AdmittedOrigin{OriginRuleId: "api", Scheme: "https", Host: "api.stripe.com", Port: 443}
}

func safeResponse() *providerv0.ExecuteRequestResponse {
	return &providerv0.ExecuteRequestResponse{
		StatusCode: 200,
		Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		Forwarded: []*providerv0.FilteredField{
			{Selector: "$.id", Value: &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: "acct_1"}}},
		},
	}
}

func TestRecordReplay_RoundTrips(t *testing.T) {
	policy := "sha256:" + repeat64('a')
	key := cassette.NewKey(plannedRequest(policy), admittedOrigin())

	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, recorder.Record(key, 200, nil, safeResponse()))

	data, err := recorder.Marshal()
	require.NoError(t, err)

	replayer, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)

	// The idempotency key differs on replay (host-injected volatile), but the
	// match still succeeds because it is normalized out of the key.
	status, response, err := replayer.Replay(mutateIdempotency(t, policy))
	require.NoError(t, err)
	require.Equal(t, uint32(200), status)
	require.Len(t, response.GetForwarded(), 1)
	require.Equal(t, "acct_1", response.GetForwarded()[0].GetValue().GetStringValue())
}

func mutateIdempotency(t *testing.T, policy string) cassette.Key {
	t.Helper()
	planned := plannedRequest(policy)
	planned.IdempotencyKey = "idem-different"
	return cassette.NewKey(planned, admittedOrigin())
}

func TestRecording_IsHumanReviewable(t *testing.T) {
	// The stored cassette must be a readable diff, not an opaque blob: the
	// selector and safe value appear as plain text.
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, recorder.Record(cassette.NewKey(plannedRequest("sha256:"+repeat64('a')), admittedOrigin()), 200, nil, safeResponse()))
	data, err := recorder.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(data), "$.id")
	require.Contains(t, string(data), "acct_1")
}

func TestRecording_IsDeterministic(t *testing.T) {
	policy := "sha256:" + repeat64('b')
	key := cassette.NewKey(plannedRequest(policy), admittedOrigin())

	first := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, first.Record(key, 200, nil, safeResponse()))
	firstBytes, err := first.Marshal()
	require.NoError(t, err)

	second := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, second.Record(key, 200, nil, safeResponse()))
	secondBytes, err := second.Marshal()
	require.NoError(t, err)

	require.Equal(t, firstBytes, secondBytes, "recording must be byte-identical")
}

func TestReplay_NoLiveFallback(t *testing.T) {
	replayer := cassette.New(cassette.ModeReplay, "1.2.3")
	key := cassette.NewKey(plannedRequest("sha256:"+repeat64('c')), admittedOrigin())
	_, _, err := replayer.Replay(key)
	require.ErrorContains(t, err, "does not fall back to live")
}

func TestReplay_PolicyMismatchRejected(t *testing.T) {
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, recorder.Record(cassette.NewKey(plannedRequest("sha256:"+repeat64('d')), admittedOrigin()), 200, nil, safeResponse()))
	data, err := recorder.Marshal()
	require.NoError(t, err)

	replayer, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)

	// Same request but a drifted response policy digest.
	_, _, err = replayer.Replay(cassette.NewKey(plannedRequest("sha256:"+repeat64('e')), admittedOrigin()))
	require.ErrorContains(t, err, "policy digest")
}

func TestReplay_ProviderVersionMismatchRejected(t *testing.T) {
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, recorder.Record(cassette.NewKey(plannedRequest("sha256:"+repeat64('f')), admittedOrigin()), 200, nil, safeResponse()))
	data, err := recorder.Marshal()
	require.NoError(t, err)

	replayer, err := cassette.Load(data, "9.9.9") // different provider version
	require.NoError(t, err)
	_, _, err = replayer.Replay(cassette.NewKey(plannedRequest("sha256:"+repeat64('f')), admittedOrigin()))
	require.ErrorContains(t, err, "provider version")
}

func TestRecord_PoisonSecretHardFailure(t *testing.T) {
	poison := &providerv0.ExecuteRequestResponse{
		StatusCode: 200,
		Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		Forwarded: []*providerv0.FilteredField{
			{Selector: "$.token", Value: &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: "sk_live_1234567890abcdef"}}},
		},
	}
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	err := recorder.Record(cassette.NewKey(plannedRequest("sha256:"+repeat64('a')), admittedOrigin()), 200, nil, poison)
	require.Error(t, err)
}

func TestRecord_RejectsSecretHeader(t *testing.T) {
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	err := recorder.Record(
		cassette.NewKey(plannedRequest("sha256:"+repeat64('a')), admittedOrigin()),
		200,
		map[string]string{"X-Trace": "Bearer sk_live_1234567890abcdef"},
		safeResponse(),
	)
	require.Error(t, err)
}

func TestReplay_RejectsRecordModeCassette(t *testing.T) {
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	_, _, err := recorder.Replay(cassette.NewKey(plannedRequest("sha256:"+repeat64('a')), admittedOrigin()))
	require.ErrorContains(t, err, "not in replay mode")
}

func repeat64(b byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
