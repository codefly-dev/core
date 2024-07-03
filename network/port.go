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
// APP-SVC-API
// Between 1100(0) and 5(9)
// First 11 -> 49: hash mod
// Next 0 -> 9: hash svc
// Next 0 - 9: hash name
// Last Digit: API
// 0: TCP
// 1: HTTP/ REST
// 2: gRPC

func ToNamedPort(ctx context.Context, ws, mod, svc, name, api string) uint16 {
	// Combine all inputs except API into a single string
	combined := strings.Join([]string{ws, mod, svc, name}, "-")

	// Use SHA-256 to get a more uniformly distributed hash
	hash := sha256.Sum256([]byte(combined))

	// Use the first 6 bytes of the hash to get a large number
	num := binary.BigEndian.Uint64(hash[:8])

	// Map this number to the range 1024-65525 (leaving room for API type)
	basePort := 1024 + (num % 64502) // 64502 is 65525 - 1024 + 1

	// Ensure the last digit is 0 to make room for the API type
	basePort = basePort - (basePort % 10)

	// Add the API type to the last digit
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
	// #nosec G204
	cmd := exec.Command("lsof", "-n", fmt.Sprintf("-i4TCP:%d", port))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(output), "\n")
	if len(lines) > 1 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 2 {
			return fields[1], nil // PID is the second field
		}
	}
	return "", nil
}

func KillProcess(pid string) error {
	pidInt, _ := strconv.Atoi(pid)
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
