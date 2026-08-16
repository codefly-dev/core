package agents

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// TestServeRegistersSolution spawns a real agent binary that registers a
// Solution server through PluginRegistration and confirms Serve exposes the
// contract: a Solution RPC over the wire returns the fixture's sentinel.
// Because the fixture implements GetSolutionInformation (rather than
// inheriting the embedded Unimplemented), a nil error + sentinel proves Serve
// registered the service and routed to the handler — deleting the Solution
// registration in Serve would surface here as an Unimplemented "unknown
// service" error, which the embedded default could not produce.
func TestServeRegistersSolution(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "solutionagent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/solutionagent")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build solution fixture: %v\n%s", err, output)
	}

	const token = "test-token"
	cmd := exec.Command(binary)
	// Empty UDS path forces the TCP loopback handshake (dns:///127.0.0.1:PORT),
	// sidestepping the OS sun_path length limit on long temp-dir socket paths.
	cmd.Env = append(os.Environ(), "CODEFLY_AGENT_TOKEN="+token, "CODEFLY_AGENT_UDS_PATH=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	endpoint := readHandshakeEndpoint(t, stdout, &stderr)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", endpoint, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, AuthMetadataKey, token)

	resp, err := solutionv0.NewSolutionClient(conn).GetSolutionInformation(ctx, &solutionv0.GetSolutionInformationRequest{})
	if err != nil {
		t.Fatalf("GetSolutionInformation: %v\nagent stderr:\n%s", err, stderr.String())
	}
	if resp.GetArtifact().GetName() != "solution-fixture" {
		t.Fatalf("Solution RPC did not route to the registered handler: %+v", resp)
	}
}

// readHandshakeEndpoint reads the "VERSION|endpoint" line the agent writes to
// stdout on startup, verifies the protocol version, and returns the endpoint.
func readHandshakeEndpoint(t *testing.T, stdout io.Reader, stderr *bytes.Buffer) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	lines := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		lines <- result{line: line, err: err}
	}()

	select {
	case r := <-lines:
		if r.err != nil {
			t.Fatalf("read handshake: %v\nagent stderr:\n%s", r.err, stderr.String())
		}
		parts := strings.SplitN(strings.TrimSpace(r.line), "|", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed handshake %q", r.line)
		}
		if version, err := strconv.Atoi(parts[0]); err != nil || version != ProtocolVersion {
			t.Fatalf("handshake version %q != %d", parts[0], ProtocolVersion)
		}
		return parts[1]
	case <-time.After(10 * time.Second):
		t.Fatalf("agent did not emit handshake within 10s\nagent stderr:\n%s", stderr.String())
		return ""
	}
}
