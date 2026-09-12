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
		// The PID namespace proves "same namespace as the container's recorded
		// owner", which is what makes a local liveness check meaningful. It is
		// not a durable name: the kernel recycles nsfs inodes, so sequential
		// containers can reuse one and concurrent containers never share one.
		// Both directions only ever decline to reap, so a stale or distinct
		// inode costs recovery, never a wrongly deleted container.
		pidNamespace, err := os.Readlink("/proc/self/ns/pid")
		if err != nil {
			return "", fmt.Errorf("identify local PID namespace: %w", err)
		}
		// gopsutil's Linux fallback is a boot ID. It changes on reboot and
		// would silently strand containers, so require a persistent identity.
		// Absence is reported as no identity rather than an error: alpine,
		// debian-slim and ubuntu images ship no usable machine ID, and failing
		// here would stop every containerized run instead of narrowing recovery
		// to the exact scope. ReapDisposableContainers traces the consequence.
		for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id", "/sys/class/dmi/id/product_uuid"} {
			data, err := os.ReadFile(path) // #nosec G304 -- fixed operating-system identity paths.
			id := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(data)), "-", ""))
			decoded, decodeErr := hex.DecodeString(id)
			if err == nil && decodeErr == nil && len(decoded) == 16 && strings.Trim(id, "0") != "" && strings.Trim(id, "f") != "" {
				return id + ":" + pidNamespace, nil
			}
		}
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	id, err := host.HostIDWithContext(ctx)
	if err != nil {
		return "", nil // Same degrade: no durable identity, not a failed run.
	}
	return id, nil
}
