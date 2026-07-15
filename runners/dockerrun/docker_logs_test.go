package dockerrun

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/pkg/stdcopy"
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
	stdout := stdcopy.NewStdWriter(&framed, stdcopy.Stdout)
	stderr := stdcopy.NewStdWriter(&framed, stdcopy.Stderr)
	_, _ = stdout.Write([]byte("ready\n"))
	_, _ = stderr.Write([]byte("warning\n"))

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
