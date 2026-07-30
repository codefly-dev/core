package broker_test

import (
	"context"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/stretchr/testify/require"
)

// TestExecute_RecordThenReplayIsEnforcementIdentical records a live create
// response, then replays it through the same broker admission path without any
// network — the replayed filtered response is identical to the recorded one and
// replay never falls back to live.
func TestExecute_RecordThenReplayIsEnforcementIdentical(t *testing.T) {
	// Record phase: live server, cassette in record mode.
	rec := newHarness(t)
	server := accountServer(t, nil)
	recordCassette := cassette.New(cassette.ModeRecord, "1.2.3")
	recCfg := rec.config(false)
	recCfg.ClientFor = dialClientFor(serverAddr(t, server))
	recCfg.Cassette = recordCassette
	recSession, err := broker.New(recCfg)
	require.NoError(t, err)

	recorded, err := recSession.Execute(context.Background(), rec.executeRequest(rec.mintHandle(t, rec.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rec.create))
	require.NoError(t, err)
	require.Equal(t, []string{poisonSecret}, rec.sink.stored)

	data, err := recordCassette.Marshal()
	require.NoError(t, err)
	require.NotContains(t, string(data), poisonSecret, "cassette must not persist the secret")

	// Replay phase: no live server reachable (closed addr), cassette in replay.
	replayCassette, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)
	rep := newHarness(t)
	repCfg := rep.config(false)
	repCfg.ClientFor = dialClientFor(reservedClosedAddr(t)) // proves no live fallback
	repCfg.Cassette = replayCassette
	repSession, err := broker.New(repCfg)
	require.NoError(t, err)

	replayed, err := repSession.Execute(context.Background(), rep.executeRequest(rep.mintHandle(t, rep.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rep.create))
	require.NoError(t, err)

	// The replay produced no live capture (the sink was untouched) yet returned
	// the identical filtered forwarded fields and capture references.
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, replayed.GetDelivery())
	require.Equal(t, recorded.GetStatusCode(), replayed.GetStatusCode())
	require.Equal(t, len(recorded.GetForwarded()), len(replayed.GetForwarded()))
	require.Equal(t, recorded.GetForwarded()[0].GetValue().GetStringValue(), replayed.GetForwarded()[0].GetValue().GetStringValue())
	require.Equal(t, len(recorded.GetCaptures()), len(replayed.GetCaptures()))
	require.Equal(t, recorded.GetSuppressedPresence(), replayed.GetSuppressedPresence())
}

// TestExecute_ReplayMissingEntryDoesNotHitNetwork proves an unrecorded request
// fails closed rather than reaching the network.
func TestExecute_ReplayMissingEntryDoesNotHitNetwork(t *testing.T) {
	empty := cassette.New(cassette.ModeReplay, "1.2.3")
	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(reservedClosedAddr(t))
	cfg.Cassette = empty
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "does not fall back to live")
}
