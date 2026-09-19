//go:build linux

package base

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/tklauser/go-sysconf"
	"golang.org/x/sys/unix"
)

var errProcessNotFound = errors.New("process not found")

// processStartUnixSeconds uses the kernel's boot timestamp, not wall clock
// minus a separately sampled uptime. Scheduling between those observations
// can otherwise make the same process appear younger on a later read.
func processStartUnixSeconds(pid int) (int64, error) {
	identity, err := readLinuxProcessStat(pid)
	if err != nil {
		return 0, err
	}
	if identity.startID > math.MaxInt64 {
		return 0, errors.New("process start ticks exceed timestamp range")
	}
	startTicks := int64(identity.startID)
	ticks, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil {
		return 0, fmt.Errorf("read process clock frequency: %w", err)
	}
	if ticks <= 0 {
		return 0, errors.New("process clock frequency is not positive")
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		boot, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || boot <= 0 {
			return 0, errors.New("invalid kernel boot timestamp")
		}
		seconds := startTicks / ticks
		if boot > math.MaxInt64-seconds {
			return 0, errors.New("process start exceeds timestamp range")
		}
		return boot + seconds, nil
	}
	return 0, errors.New("kernel boot timestamp is missing")
}

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

// readProcessGroupAuthentication reads pid's start credential and reports
// whether pid's environment was observable at all. Linux exposes it through
// /proc/<pid>/environ for any process we may ptrace, which includes every
// same-user descendant this package starts, so a read that succeeds has
// observed the environment.
func readProcessGroupAuthentication(pid int) (string, bool, error) {
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, errProcessNotFound
		}
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4<<20))
	if err != nil {
		return "", false, err
	}
	prefix := []byte(groupAuthEnv + "=")
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if value, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(value), true, nil
		}
	}
	return "", true, nil
}

type linuxProcessSignalHandle struct {
	fd int
}

func openProcessSignalHandle(expected processIdentity) (processSignalHandle, error) {
	fd, err := unix.PidfdOpen(expected.pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return nil, errProcessNotFound
	}
	if err != nil {
		return nil, err
	}
	current, err := inspectProcess(expected.pid)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if current.pgid != expected.pgid || !current.matches(expected.recorded()) {
		_ = unix.Close(fd)
		return nil, errProcessGroupIdentityChanged
	}
	return &linuxProcessSignalHandle{fd: fd}, nil
}

func (handle *linuxProcessSignalHandle) Signal(signal syscall.Signal) error {
	return unix.PidfdSendSignal(handle.fd, unix.Signal(signal), nil, 0)
}

func (handle *linuxProcessSignalHandle) Close() error {
	return unix.Close(handle.fd)
}
