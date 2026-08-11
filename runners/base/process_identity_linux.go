//go:build linux

package base

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tklauser/go-sysconf"
	"golang.org/x/sys/unix"
)

var errProcessNotFound = errors.New("process not found")

type processIdentity struct {
	pid        int
	pgid       int
	bootID     string
	startID    uint64
	executable string
}

func inspectProcess(pid int) (processIdentity, error) {
	first, err := readLinuxProcessStat(pid)
	if err != nil {
		return processIdentity{}, err
	}
	executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		if os.IsNotExist(err) {
			return processIdentity{}, errProcessNotFound
		}
		return processIdentity{}, err
	}
	second, err := readLinuxProcessStat(pid)
	if err != nil {
		return processIdentity{}, err
	}
	if first.pgid != second.pgid || first.startID != second.startID {
		return processIdentity{}, errors.New("process identity changed while being inspected")
	}
	bootID, err := linuxBootID()
	if err != nil {
		return processIdentity{}, err
	}
	first.bootID = bootID
	first.executable = sanitizeExecutableIdentity(executable)
	if first.executable == "" {
		return processIdentity{}, errors.New("process executable identity is empty")
	}
	return first, nil
}

func readLinuxProcessStat(pid int) (processIdentity, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if os.IsNotExist(err) {
			return processIdentity{}, errProcessNotFound
		}
		return processIdentity{}, err
	}
	closing := strings.LastIndex(string(data), ") ")
	if closing < 0 {
		return processIdentity{}, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[closing+2:]))
	if len(fields) < 20 {
		return processIdentity{}, errors.New("incomplete process stat")
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		return processIdentity{}, fmt.Errorf("parse process group: %w", err)
	}
	startID, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processIdentity{}, fmt.Errorf("parse process start identity: %w", err)
	}
	if startID == 0 {
		return processIdentity{}, errors.New("process start identity is zero")
	}
	return processIdentity{pid: pid, pgid: pgid, startID: startID}, nil
}

func currentProcessBoundary() (processBoundary, error) {
	bootID, err := linuxBootID()
	if err != nil {
		return processBoundary{}, err
	}
	clockTicks, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil {
		return processBoundary{}, fmt.Errorf("read clock ticks: %w", err)
	}
	if clockTicks <= 0 {
		return processBoundary{}, errors.New("clock ticks must be positive")
	}
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &now); err != nil {
		return processBoundary{}, err
	}
	startID := uint64(now.Sec)*uint64(clockTicks) + uint64(now.Nsec)*uint64(clockTicks)/1_000_000_000
	return processBoundary{BootID: bootID, StartID: startID}, nil
}

func linuxBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	bootID := strings.TrimSpace(string(data))
	if bootID == "" {
		return "", errors.New("boot identity is empty")
	}
	return bootID, nil
}
