package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/standards"
)

type FixedStrategy struct {
}

func HashInt(s string, low, high int) int {
	hasher := sha256.New()
	hasher.Write([]byte(s))
	hash := hasher.Sum(nil)
	num := binary.BigEndian.Uint32(hash)
	return int(num%uint32(high-low)) + low
}

func APIInt(api string) int {
	switch api {
	case standards.TCP:
		return 0
	case standards.HTTP:
		return 1
	case standards.REST:
		return 2
	case standards.GRPC:
		return 3
	default:
		return 0
	}
}

// ToNamedPort strategy:
// APP-SVC-GetAPI
// Between 1100(0) and 5(9)
// First 11 -> 49: hash mod
// Next 0 -> 9: hash svc
// Next 0 - 9: hash name
// Last Digit: GetAPI
// 0: TCP
// 1: HTTP/ REST
// 2: gRPC

// CLIServerPort returns a deterministic TCP port for the codefly CLI
// gRPC server of a given workspace. Same workspace name → same port
// across runs (so tools like Postman can hit a stable endpoint).
// Different workspaces → different ports (so concurrent test runs in
// unrelated workspaces don't collide on 10000).
//
// The port lives in [20000, 29900] to stay clear of ephemeral ranges
// and common service ports. The REST companion port lives at +1.
//
// Override with the CODEFLY_CLI_SERVER_PORT environment variable when
// an explicit port is required.
func CLIServerPort(workspaceName string) uint16 {
	if v := os.Getenv("CODEFLY_CLI_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return uint16(p)
		}
	}
	// Align to a multiple of 10 so CLIRestPort (= CLIServerPort+1) is
	// stable and readable.
	base := HashInt(workspaceName, 20000, 29900)
	base = base - (base % 10)
	return uint16(base)
}

// CLIRestPort is the REST companion port for the CLI gRPC server.
// Always CLIServerPort + 1 so the pair stays together.
func CLIRestPort(workspaceName string) uint16 {
	return CLIServerPort(workspaceName) + 1
}

func ToNamedPort(_ context.Context, ws, mod, svc, name, api string) uint16 {
	// Combine all inputs except GetAPI into a single string
	combined := strings.Join([]string{ws, mod, svc, name}, "-")

	// Use SHA-256 to get a more uniformly distributed hash
	hash := sha256.Sum256([]byte(combined))

	// Use the first 6 bytes of the hash to get a large number
	num := binary.BigEndian.Uint64(hash[:8])

	// Map this number to the range 1024-65525 (leaving room for GetAPI type)
	basePort := 1024 + (num % 64502) // 64502 is 65525 - 1024 + 1

	// Ensure the last digit is 0 to make room for the GetAPI type
	basePort = basePort - (basePort % 10)

	// Add the GetAPI type to the last digit
	return uint16(basePort) + uint16(APIInt(api))
}

func IsPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func KillProcessUsingPort(port int) error {
	pid, err := GetPidUsingPort(port)
	if err != nil {
		return err
	}
	if pid != "" {
		return KillProcess(pid)
	}
	return nil
}

func GetPidUsingPort(port int) (string, error) {
	// lsof's TCP-port flag takes a typed numeric port — not raw user
	// input — so the gosec G204 warning is a false positive here.
	// Standardized on //nolint:gosec to match the rest of the file
	// (lines 235-236 use this form).
	//nolint:gosec // G204: int port is type-safe input, not tainted
	cmd := exec.Command("lsof", "-n", fmt.Sprintf("-i4TCP:%d", port))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	// lsof's tabular output is:
	//   COMMAND   PID  USER  FD  TYPE  DEVICE  SIZE/OFF  NODE  NAME
	// Header on line 0, first match on line 1+. Parse defensively —
	// a future lsof version that adds/reorders columns would silently
	// return the wrong field without these checks.
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		// Either empty output (nothing listening) or just the
		// header; both mean "no PID found", not an error.
		return "", nil
	}
	const expectedMinFields = 9 // 9 columns in lsof's standard output
	const pidField = 1          // PID is the second column
	fields := strings.Fields(lines[1])
	if len(fields) < expectedMinFields {
		return "", fmt.Errorf("unexpected lsof output: got %d fields, want >= %d. Line: %q",
			len(fields), expectedMinFields, lines[1])
	}
	return fields[pidField], nil
}

func KillProcess(pid string) error {
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		return fmt.Errorf("invalid PID %q: %w", pid, err)
	}
	if pidInt <= 0 {
		return fmt.Errorf("invalid PID %d: must be positive", pidInt)
	}
	process, err := os.FindProcess(pidInt)
	if err != nil {
		return err
	}
	err = process.Kill()
	if err != nil {
		return err
	}
	return nil
}
