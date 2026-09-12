package dockerrun

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

func containerRecoveryHostID() (string, error) {
	const linuxOS = "linux"
	if runtime.GOOS == linuxOS {
		pidNamespace, err := os.Readlink("/proc/self/ns/pid")
		if err != nil {
			return "", fmt.Errorf("identify local PID namespace: %w", err)
		}
		// gopsutil's Linux fallback is a boot ID. It changes on reboot and
		// would silently strand containers, so require a persistent identity.
		for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id", "/sys/class/dmi/id/product_uuid"} {
			data, err := os.ReadFile(path) // #nosec G304 -- fixed operating-system identity paths.
			id := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(data)), "-", ""))
			decoded, decodeErr := hex.DecodeString(id)
			if err == nil && decodeErr == nil && len(decoded) == 16 && strings.Trim(id, "0") != "" && strings.Trim(id, "f") != "" {
				return id + ":" + pidNamespace, nil
			}
		}
		return "", fmt.Errorf("no persistent machine ID; provision /etc/machine-id before using Docker recovery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return host.HostIDWithContext(ctx)
}
