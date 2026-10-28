//go:build darwin

package base

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func recoveryLegacyBootMatches(recorded string) (bool, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return false, err
	}
	// Exact historical formatting: no normalization or translation to a UUID.
	return recorded == fmt.Sprintf("%d:%d", boot.Sec, boot.Usec), nil
}
