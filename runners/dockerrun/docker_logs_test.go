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

func TestContainerConfigPreservesExplicitUser(t *testing.T) {
	env := &DockerEnvironment{image: resources.NewDockerImage("example:1")}
	env.WithUser("1001:1002")

	config := env.createContainerConfig(context.Background())

	if config.User != "1001:1002" {
		t.Fatalf("container user = %q, want 1001:1002", config.User)
	}
	command := generateDockerCreateCommand(config, env.createHostConfig(context.Background()), "companion")
	if !strings.Contains(command, "--user 1001:1002") {
		t.Fatalf("diagnostic Docker command omitted explicit user: %s", command)
	}
}

func TestForwardContainerOutputDemultiplexesDockerFrames(t *testing.T) {
	var framed bytes.Buffer
	writeDockerFrame(&framed, stdcopy.Stdout, []byte("ready\n"))
	writeDockerFrame(&framed, stdcopy.Stderr, []byte("warning\n"))

	var output bytes.Buffer
	forwardContainerOutput(context.Background(), &framed, &output, false)

	if got := output.String(); got != "ready\nwarning\n" {
		t.Fatalf("demultiplexed output = %q", got)
	}
}

// writeDockerFrame encodes the wire format consumed by stdcopy.StdCopy. Moby's
// split API module intentionally exposes the demultiplexer but not the daemon's
// internal framing writer, so this test constructs the eight-byte protocol
// header directly.
func writeDockerFrame(dst *bytes.Buffer, stream stdcopy.StdType, payload []byte) {
	var header [8]byte
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = dst.Write(header[:])
	_, _ = dst.Write(payload)
}

func TestForwardContainerOutputSupportsLegacyTTYAndStripsControl(t *testing.T) {
	raw := strings.NewReader("\x1b[2Jready\n")
	var output bytes.Buffer

	forwardContainerOutput(context.Background(), raw, &output, true)

	if got := output.String(); got != "ready\n" {
		t.Fatalf("legacy TTY output = %q", got)
	}
}
