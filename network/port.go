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

	"github.com/codefly-dev/core/configurations/standards"
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
// Between 1100(0) and 4999(9)
// First 11 -> 49: hash app
// Next 0 -> 9: hash svc
// Next 0 - 9: hash name
// Last Digit: API
// 0: TCP
// 1: HTTP/ REST
// 2: gRPC
func ToNamedPort(_ context.Context, project string, app string, svc string, name string, api string) uint16 {
	appPart := HashInt(app+project, 11, 49) * 1000
	svcPart := HashInt(app+svc, 0, 9) * 100
	namePart := HashInt(name, 0, 9) * 10
	port := appPart + svcPart + namePart + APIInt(api)
	return uint16(port)
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
