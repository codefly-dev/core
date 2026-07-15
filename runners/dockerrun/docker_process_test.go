package dockerrun

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestDockerProcMatchUsesExactExecutableBasename(t *testing.T) {
	proc := &DockerProc{cmd: []string{"/bin/sh"}}
	if !proc.Match([]string{"sh", "script.sh"}) {
		t.Fatal("equivalent executable basenames did not match")
	}
	if proc.Match([]string{"/usr/bin/bash"}) || proc.Match([]string{"/tmp/sh-helper"}) {
		t.Fatal("substring-related executable matched")
	}

	proc.waitOn = "server"
	if !proc.Match([]string{"/usr/local/bin/server", "--listen"}) {
		t.Fatal("WaitOn executable basename did not match")
	}
}

func TestDockerProcPIDWrapperPreservesArguments(t *testing.T) {
	proc := &DockerProc{cmd: []string{"printf", "%s", "value with spaces"}}
	command := proc.prepareCommand()

	if len(command) < len(proc.cmd) || proc.pidFile == "" {
		t.Fatalf("missing PID wrapper: command=%#v pidFile=%q", command, proc.pidFile)
	}
	if got := command[len(command)-len(proc.cmd):]; strings.Join(got, "\x00") != strings.Join(proc.cmd, "\x00") {
		t.Fatalf("wrapped args = %#v, want %#v", got, proc.cmd)
	}
}

func TestRedactedContainerConfigHidesSecretsAndCommandArguments(t *testing.T) {
	config := &container.Config{
		Env: []string{"CLICKHOUSE_PASSWORD=hunter2", "API_TOKEN=token-value", "NORMAL=value"},
		Cmd: []string{"redis-server", "--requirepass", "hunter2"},
	}
	redacted := redactedContainerConfig(config)
	serialized := strings.Join(append(redacted.Env, redacted.Cmd...), " ")
	if strings.Contains(serialized, "hunter2") || strings.Contains(serialized, "token-value") {
		t.Fatalf("redacted config leaked a secret: %s", serialized)
	}
	if !strings.Contains(serialized, "NORMAL=value") || !strings.Contains(serialized, "CLICKHOUSE_PASSWORD=****") {
		t.Fatalf("redacted config lost safe fields or markers: %s", serialized)
	}
	if config.Env[0] != "CLICKHOUSE_PASSWORD=hunter2" || config.Cmd[1] != "--requirepass" {
		t.Fatal("redaction mutated the real container config")
	}
}

func TestPrintDownloadPercentageReturnsRegistryError(t *testing.T) {
	stream := io.NopCloser(strings.NewReader("{\"errorDetail\":{\"message\":\"manifest unknown\"},\"error\":\"manifest unknown\"}\n"))
	err := PrintDownloadPercentage(stream, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("registry stream error = %v", err)
	}
}

func TestPrintDownloadPercentageRejectsMalformedStream(t *testing.T) {
	stream := io.NopCloser(strings.NewReader("not-json\n"))
	if err := PrintDownloadPercentage(stream, &bytes.Buffer{}); err == nil {
		t.Fatal("malformed pull stream was reported as success")
	}
}
