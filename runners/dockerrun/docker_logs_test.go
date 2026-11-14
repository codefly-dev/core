package dockerrun

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/moby/moby/api/pkg/stdcopy"
)

func TestContainerConfigDisablesPseudoTTY(t *testing.T) {
	env := &DockerEnvironment{image: resources.NewDockerImage("example:1")}

	config := env.createContainerConfig(context.Background())

	if config.Tty {
		t.Fatal("background service container unexpectedly allocated a pseudo-TTY")
	}
}

func TestForwardContainerOutputDemultiplexesDockerFrames(t *testing.T) {
	var framed bytes.Buffer
	writeStdFrame(&framed, stdcopy.Stdout, "ready\n")
	writeStdFrame(&framed, stdcopy.Stderr, "warning\n")

	var output bytes.Buffer
	forwardContainerOutput(context.Background(), &framed, &output, false)

	if got := output.String(); got != "ready\nwarning\n" {
		t.Fatalf("demultiplexed output = %q", got)
	}
}

func TestForwardContainerOutputSupportsLegacyTTYAndStripsControl(t *testing.T) {
	raw := strings.NewReader("\x1b[2Jready\n")
	var output bytes.Buffer

	forwardContainerOutput(context.Background(), raw, &output, true)

	if got := output.String(); got != "ready\n" {
		t.Fatalf("legacy TTY output = %q", got)
	}
}

// writeStdFrame writes one frame of Docker's multiplexed log stream: an
// eight-byte header (stream, three zero bytes, big-endian payload length)
// followed by the payload.
func writeStdFrame(w *bytes.Buffer, stream stdcopy.StdType, payload string) {
	header := [8]byte{byte(stream)}
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	w.Write(header[:])
	w.WriteString(payload)
}
