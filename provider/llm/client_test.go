package llm_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/codefly-dev/core/provider/llm"
	"github.com/stretchr/testify/require"
)

const anthropicStream = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n" +
	"\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n" +
	"\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n" +
	"\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n" +
	"\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n" +
	"\n"

const anthropicMessage = `{"id":"msg_01","type":"message","role":"assistant",` +
	`"content":[{"type":"text","text":"Hello world"}],"model":"claude-sonnet-4-5",` +
	`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

const embeddingBody = `{"object":"list","data":[{"embedding":[0.1,-0.2,0.3]}],"usage":{"input_tokens":4}}`

func jsonServer(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func chatRequest(t *testing.T, stream bool, key string) *providerv0.PlannedRequest {
	t.Helper()
	m, err := llm.Manifest(testOrigin())
	require.NoError(t, err)
	origin := admittedOrigin(t)
	planned, err := llm.PlannedChat(m, origin, llm.ChatRequest{
		Model:     model,
		Messages:  []llm.Message{{Role: "user", Content: "Hi"}},
		MaxTokens: 1024,
		Stream:    stream,
	}, key, policyD)
	require.NoError(t, err)
	return planned
}

// TestChat_NonStreamingRecordReplay records and replays a whole non-streaming
// chat completion through the broker deterministically, with no network on
// replay.
func TestChat_NonStreamingRecordReplay(t *testing.T) {
	req := chatRequest(t, false, "idem-chat")
	server := jsonServer(t, "application/json", anthropicMessage)

	recCass := cassette.New(cassette.ModeRecord, "0.1.0")
	rec := newHarness(t, req)
	recClient := llm.NewClient(rec.session(t, serverAddr(t, server), recCass))
	recorded, err := recClient.Chat(context.Background(), rec.request(t))
	require.NoError(t, err)
	require.Equal(t, "Hello world", recorded.Text)
	require.Equal(t, "end_turn", recorded.StopReason)
	require.Equal(t, int64(10), recorded.Usage.InputTokens)
	require.Equal(t, int64(5), recorded.Usage.OutputTokens)

	data, err := recCass.Marshal()
	require.NoError(t, err)

	replayCass, err := cassette.Load(data, "0.1.0")
	require.NoError(t, err)
	rep := newHarness(t, req)
	repClient := llm.NewClient(rep.session(t, reservedClosedAddr(t), replayCass))
	replayed, err := repClient.Chat(context.Background(), rep.request(t))
	require.NoError(t, err)
	require.Equal(t, recorded, replayed)
}

// TestChat_StreamingRecordReplay records and replays an SSE chat stream: the
// typed deltas, assembled text, ordering, terminal event, and reconstructed SSE
// framing all reproduce deterministically with no network on replay.
func TestChat_StreamingRecordReplay(t *testing.T) {
	req := chatRequest(t, true, "idem-stream")
	server := jsonServer(t, "text/event-stream", anthropicStream)

	recCass := cassette.New(cassette.ModeRecord, "0.1.0")
	rec := newHarness(t, req)
	recSession := rec.session(t, serverAddr(t, server), recCass)
	recorded, err := recSession.Execute(context.Background(), rec.request(t))
	require.NoError(t, err)

	recStream, err := llm.DecodeChatStream(recorded)
	require.NoError(t, err)
	require.Equal(t, "Hello world", recStream.Text)
	require.Equal(t, "end_turn", recStream.StopReason)
	require.Equal(t, int64(10), recStream.Usage.InputTokens)
	require.Equal(t, int64(5), recStream.Usage.OutputTokens)
	require.Equal(t,
		[]string{"message_start", "content_block_delta", "content_block_delta", "message_delta", "message_stop"},
		deltaTypes(recStream))
	require.True(t, recStream.Deltas[len(recStream.Deltas)-1].Terminal)

	firstFraming := llm.ReconstructSSE(recorded.GetEvents())

	data, err := recCass.Marshal()
	require.NoError(t, err)
	require.NotContains(t, string(data), apiKey)

	replayCass, err := cassette.Load(data, "0.1.0")
	require.NoError(t, err)
	rep := newHarness(t, req)
	repSession := rep.session(t, reservedClosedAddr(t), replayCass)
	replayed, err := repSession.Execute(context.Background(), rep.request(t))
	require.NoError(t, err)

	repStream, err := llm.DecodeChatStream(replayed)
	require.NoError(t, err)
	require.Equal(t, recStream, repStream)

	// Framing is reproduced byte-for-byte, terminated by the message_stop event.
	replayFraming := llm.ReconstructSSE(replayed.GetEvents())
	require.Equal(t, string(firstFraming), string(replayFraming))
	require.True(t, strings.Contains(string(replayFraming), "event: message_stop"))
	require.True(t, strings.HasSuffix(string(replayFraming), "\n\n"))
}

// TestEmbed_RecordReplay records and replays an embedding call deterministically.
func TestEmbed_RecordReplay(t *testing.T) {
	m, err := llm.Manifest(testOrigin())
	require.NoError(t, err)
	origin := admittedOrigin(t)
	planned, err := llm.PlannedEmbed(m, origin, llm.EmbedRequest{
		Model: "voyage-3",
		Input: []string{"hello"},
	}, "idem-embed", policyD)
	require.NoError(t, err)
	server := jsonServer(t, "application/json", embeddingBody)

	recCass := cassette.New(cassette.ModeRecord, "0.1.0")
	rec := newHarness(t, planned)
	recClient := llm.NewClient(rec.session(t, serverAddr(t, server), recCass))
	recorded, err := recClient.Embed(context.Background(), rec.request(t))
	require.NoError(t, err)
	require.Len(t, recorded.Embeddings, 1)
	require.Equal(t, llm.Embedding{0.1, -0.2, 0.3}, recorded.Embeddings[0])
	require.Equal(t, int64(4), recorded.Usage.InputTokens)

	data, err := recCass.Marshal()
	require.NoError(t, err)

	replayCass, err := cassette.Load(data, "0.1.0")
	require.NoError(t, err)
	rep := newHarness(t, planned)
	repClient := llm.NewClient(rep.session(t, reservedClosedAddr(t), replayCass))
	replayed, err := repClient.Embed(context.Background(), rep.request(t))
	require.NoError(t, err)
	require.Equal(t, recorded, replayed)
}

// TestChat_StreamReplayUnknownKeyFailsClosed proves streaming replay never falls
// back to live: an empty cassette hard-errors instead of reaching the network.
func TestChat_StreamReplayUnknownKeyFailsClosed(t *testing.T) {
	req := chatRequest(t, true, "idem-stream")
	empty := cassette.New(cassette.ModeReplay, "0.1.0")
	h := newHarness(t, req)
	client := llm.NewClient(h.session(t, reservedClosedAddr(t), empty))
	_, err := client.ChatStream(context.Background(), h.request(t))
	require.ErrorContains(t, err, "does not fall back to live")
}

func deltaTypes(stream *llm.ChatStream) []string {
	types := make([]string, 0, len(stream.Deltas))
	for _, delta := range stream.Deltas {
		types = append(types, delta.EventType)
	}
	return types
}
