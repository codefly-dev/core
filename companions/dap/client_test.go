package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These wire exchanges exercise the maintained client and transport without
// starting an adapter, a companion, or a host service. The real debugpy test
// remains separately required under python_companion_required.
func TestStartDebugSessionProtocol(t *testing.T) {
	for _, command := range []string{"launch", "attach"} {
		for _, behavior := range []string{"deferred", "early", "rejected", "configuration-error", "disconnect", "cancel"} {
			t.Run(command+"/"+behavior, func(t *testing.T) {
				var args []string
				var env map[string]string
				if behavior == "early" {
					args = []string{"--example", "value"}
					env = map[string]string{"EXAMPLE": "value"}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				conn, peer := net.Pipe()
				t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
				client := &companionClient{tp: newTransport(ctx, conn)}
				peerDone := make(chan struct{})
				go func() {
					defer close(peerDone)
					reader := bufio.NewReader(peer)
					start := readProtocolRequest(t, reader)
					require.Equal(t, command, start.Command)
					if command == "launch" {
						wire := start.Arguments.(map[string]interface{})
						t.Logf("launch wire arguments: %v", wire)
						if args == nil {
							require.NotContains(t, wire, "args")
							require.NotContains(t, wire, "env")
						} else {
							require.Equal(t, []interface{}{"--example", "value"}, wire["args"])
							require.Equal(t, map[string]interface{}{"EXAMPLE": "value"}, wire["env"])
						}
					}
					if behavior == "cancel" {
						cancel()
						return
					}
					if behavior == "disconnect" {
						_ = peer.Close()
						return
					}
					if behavior == "rejected" {
						writeProtocolMessage(t, peer, dapResponse{Type: "response", RequestSeq: start.Seq, Success: false, Message: "start refused"})
						return
					}
					if behavior == "early" {
						writeProtocolMessage(t, peer, dapResponse{Type: "response", RequestSeq: start.Seq, Success: true})
					}
					writeProtocolMessage(t, peer, dapEvent{Type: "event", Event: "initialized"})
					configuration := readProtocolRequest(t, reader)
					require.Equal(t, "configurationDone", configuration.Command)
					writeProtocolMessage(t, peer, dapResponse{Type: "response", RequestSeq: configuration.Seq,
						Success: behavior != "configuration-error", Message: "configuration refused"})
					if behavior != "early" && behavior != "configuration-error" {
						writeProtocolMessage(t, peer, dapResponse{Type: "response", RequestSeq: start.Seq, Success: true})
					}
				}()
				var err error
				if command == "launch" {
					err = client.Launch(ctx, "/workspace/main.py", args, env)
				} else {
					err = client.Attach(ctx, 42)
				}
				switch behavior {
				case "deferred", "early":
					require.NoError(t, err)
				case "rejected":
					require.ErrorContains(t, err, "start refused")
				case "configuration-error":
					require.ErrorContains(t, err, "configuration refused")
				default:
					require.Error(t, err)
				}
				<-peerDone
			})
		}
	}
}

func TestConnectionProbePreservesStartupEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
	go writeProtocolMessage(t, peer, dapEvent{Type: "event", Event: "initialized"})
	ready, err := probeConnection(conn)
	require.NoError(t, err)
	transport := newTransport(ctx, ready)
	select {
	case <-transport.initialized:
	case <-ctx.Done():
		t.Fatal("connection probe discarded the first byte of the startup DAP event")
	}
}

func readProtocolRequest(t *testing.T, reader *bufio.Reader) dapRequest {
	t.Helper()
	headers, err := (&transport{reader: reader}).readHeader()
	require.NoError(t, err)
	length, err := strconv.Atoi(headers["Content-Length"])
	require.NoError(t, err)
	body := make([]byte, length)
	_, err = io.ReadFull(reader, body)
	require.NoError(t, err)
	var request dapRequest
	require.NoError(t, json.Unmarshal(body, &request))
	return request
}

func writeProtocolMessage(t *testing.T, writer io.Writer, message interface{}) {
	t.Helper()
	body, err := json.Marshal(message)
	require.NoError(t, err)
	_, err = fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(body), body)
	require.NoError(t, err)
}
