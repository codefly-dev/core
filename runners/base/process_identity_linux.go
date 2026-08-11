//go:build linux

package base

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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

func readProcessGroupAuthentication(pid int) (string, error) {
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errProcessNotFound
		}
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4<<20))
	if err != nil {
		return "", err
	}
	prefix := []byte(groupAuthEnv + "=")
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if value, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(value), nil
		}
	}
	return "", nil
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
