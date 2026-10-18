package solutionhost_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// The manifest path classified a bad payload before the envelope existed, and
// other repos branch on that classification and match its text. Adding a
// second document must not move either: these pin what b81d92e6 answered.
func TestAManifestCallersRefusalsDidNotMoveWhenTheEnvelopeArrived(t *testing.T) {
	signer := signerFor(t, testKeyID)
	envelope := json.RawMessage(`{"payloadType":"x","payload":"e30=","signatures":[{"keyid":"release","sig":"e30="}]}`)
	const readsManifest = `(this Core reads "codefly/module-manifest/v1")`

	// A manifest written as YAML parses, so it is refused as not canonical.
	_, err := solutionhost.Carrier([]byte(validYAML), envelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.ErrorIs(t, err, solutionhost.ErrNotCanonical)

	// Bytes that are no document at all are an invalid manifest.
	_, err = solutionhost.Carrier([]byte("{: ["), envelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.NotErrorIs(t, err, solutionhost.ErrSchema)

	// An unknown schema is refused in the manifest's words, by the carrier, by
	// the signer and by the verifier.
	unknown := []byte(`{"schema":"codefly/other/v1"}`)
	_, err = solutionhost.Carrier(unknown, envelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.ErrorContains(t, err, readsManifest)

	_, err = solutionhost.SignEnvelope(unknown, signer)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.ErrorContains(t, err, readsManifest)

	carrier := &solutionhost.Signed{Document: unknown, Envelope: json.RawMessage(`{"payloadType":"` + solutionhost.PayloadTypeModuleManifest +
		`","payload":"` + base64.StdEncoding.EncodeToString(unknown) + `","signatures":[{"keyid":"release","sig":"e30="}]}`)}
	_, err = solutionhost.VerifyDelivered(context.Background(), carrier, testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.ErrorContains(t, err, readsManifest)
}
