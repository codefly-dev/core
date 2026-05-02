package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/agents"
)

// TestParseAgentHandshake_LegacyNumericPort covers the original
// VERSION|PORT form emitted by plugins compiled before UDS support
// landed. New hosts must keep dialing them — anything else would
// silently break old plugin binaries on disk.
func TestParseAgentHandshake_LegacyNumericPort(t *testing.T) {
	line := versionPrefix() + "|54321"
	addr, err := parseAgentHandshake(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "127.0.0.1:54321" {
		t.Fatalf("legacy numeric port should resolve to TCP loopback, got %q", addr)
	}
}

// TestParseAgentHandshake_UDS verifies the UDS handshake form. The
// path is passed through verbatim so grpc.NewClient's `unix` resolver
// can dial it directly.
func TestParseAgentHandshake_UDS(t *testing.T) {
	line := versionPrefix() + "|unix:/tmp/codefly-uds/agent-1234.sock"
	addr, err := parseAgentHandshake(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "unix:/tmp/codefly-uds/agent-1234.sock" {
		t.Fatalf("UDS endpoint should be passed verbatim to grpc.NewClient, got %q", addr)
	}
}

func TestParseAgentHandshake_VersionMismatch(t *testing.T) {
	// Build a deliberately wrong version to exercise the error path.
	wrong := agents.ProtocolVersion + 99
	line := itoa(wrong) + "|12345"
	_, err := parseAgentHandshake(line)
	if err == nil {
		t.Fatalf("expected version mismatch error, got nil")
	}
	if !errors.Is(err, errAgentVersionMismatch) {
		t.Fatalf("expected errAgentVersionMismatch, got %T: %v", err, err)
	}
}

func TestParseAgentHandshake_Malformed(t *testing.T) {
	// Lines where the failure is in the endpoint half (not the
	// version half). A non-numeric version surfaces as
	// errAgentVersionMismatch and is covered separately above.
	cases := []struct {
		name string
		line string
	}{
		{"missing pipe", "1 54321"},
		{"port out of range", versionPrefix() + "|99999"},
		{"port zero", versionPrefix() + "|0"},
		{"port negative", versionPrefix() + "|-1"},
		{"garbage endpoint", versionPrefix() + "|tcp://x:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAgentHandshake(tc.line)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.line)
			}
			if errors.Is(err, errAgentVersionMismatch) {
				t.Fatalf("endpoint-half malformed shouldn't surface as version mismatch: %v", err)
			}
		})
	}
}

func TestParseAgentHandshake_UDS_AnyPath(t *testing.T) {
	// Paths with weird characters (spaces, dots, dashes) should still
	// pass through verbatim — the host doesn't validate path syntax,
	// only that the prefix is unix:.
	line := versionPrefix() + "|unix:/var/folders/xx/spaces here/agent.sock"
	addr, err := parseAgentHandshake(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(addr, "unix:") || !strings.Contains(addr, "spaces here") {
		t.Fatalf("UDS path mangled: got %q", addr)
	}
}

func versionPrefix() string { return itoa(agents.ProtocolVersion) }

func itoa(i int) string {
	// Tiny helper — strconv would also work but keeps the test deps
	// minimal (only stdlib + agents).
	if i == 0 {
		return "0"
	}
	negative := false
	if i < 0 {
		negative = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
