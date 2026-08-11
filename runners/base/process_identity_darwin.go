//go:build darwin

package base

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"
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
	info, err := readDarwinProcessInfo(pid)
	if err != nil {
		return processIdentity{}, err
	}
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return processIdentity{}, errProcessNotFound
		}
		return processIdentity{}, err
	}
	executablePath, err := proc.Exe()
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return processIdentity{}, errProcessNotFound
		}
		return processIdentity{}, err
	}
	verified, err := readDarwinProcessInfo(pid)
	if err != nil {
		return processIdentity{}, err
	}
	if info.Eproc.Pgid != verified.Eproc.Pgid || info.Proc.P_starttime != verified.Proc.P_starttime {
		return processIdentity{}, errors.New("process identity changed while being inspected")
	}
	bootID, err := darwinBootID()
	if err != nil {
		return processIdentity{}, err
	}
	executable := sanitizeExecutableIdentity(executablePath)
	if executable == "" {
		return processIdentity{}, errors.New("process executable identity is empty")
	}
	return processIdentity{
		pid:        pid,
		pgid:       int(info.Eproc.Pgid),
		bootID:     bootID,
		startID:    uint64(info.Proc.P_starttime.Sec)*1_000_000 + uint64(info.Proc.P_starttime.Usec),
		executable: executable,
	}, nil
}

func readDarwinProcessInfo(pid int) (*unix.KinfoProc, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EIO) {
			return nil, errProcessNotFound
		}
		return nil, err
	}
	if info.Proc.P_pid != int32(pid) {
		return nil, errProcessNotFound
	}
	if info.Proc.P_starttime.Sec <= 0 || info.Proc.P_starttime.Usec < 0 {
		return nil, errors.New("invalid process start identity")
	}
	return info, nil
}

func darwinBootID() (string, error) {
	bootTime, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", bootTime.Sec, bootTime.Usec), nil
}

func readProcessGroupAuthentication(pid int) (string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
			return "", errProcessNotFound
		}
		return "", err
	}
	if len(data) < 4 {
		return "", errors.New("process arguments are incomplete")
	}
	argc := int(binary.NativeEndian.Uint32(data[:4]))
	data = data[4:]
	executableEnd := bytes.IndexByte(data, 0)
	if executableEnd < 0 {
		return "", errors.New("process executable is unterminated")
	}
	data = bytes.TrimLeft(data[executableEnd+1:], "\x00")
	for range argc {
		argumentEnd := bytes.IndexByte(data, 0)
		if argumentEnd < 0 {
			return "", errors.New("process arguments are unterminated")
		}
		data = data[argumentEnd+1:]
	}
	prefix := []byte(groupAuthEnv + "=")
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if value, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(value), nil
		}
	}
	return "", nil
}

type darwinProcessSignalHandle struct {
	queue    int
	identity processIdentity
}

func openProcessSignalHandle(expected processIdentity) (processSignalHandle, error) {
	queue, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	var event unix.Kevent_t
	unix.SetKevent(&event, expected.pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ENABLE|unix.EV_ONESHOT)
	event.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(queue, []unix.Kevent_t{event}, nil, nil); err != nil {
		_ = unix.Close(queue)
		if errors.Is(err, syscall.ESRCH) {
			return nil, errProcessNotFound
		}
		return nil, err
	}
	current, err := inspectProcess(expected.pid)
	if err != nil {
		_ = unix.Close(queue)
		return nil, err
	}
	if current.pgid != expected.pgid || !current.matches(expected.recorded()) {
		_ = unix.Close(queue)
		return nil, errProcessGroupIdentityChanged
	}
	return &darwinProcessSignalHandle{queue: queue, identity: expected}, nil
}

func (handle *darwinProcessSignalHandle) Signal(signal syscall.Signal) error {
	events := make([]unix.Kevent_t, 1)
	timeout := unix.Timespec{}
	if count, err := unix.Kevent(handle.queue, nil, events, &timeout); err != nil {
		return err
	} else if count > 0 {
		return syscall.ESRCH
	}
	current, err := inspectProcess(handle.identity.pid)
	if err != nil {
		return err
	}
	if current.pgid != handle.identity.pgid || !current.matches(handle.identity.recorded()) {
		return errProcessGroupIdentityChanged
	}
	return syscall.Kill(handle.identity.pid, signal)
}

func (handle *darwinProcessSignalHandle) Close() error {
	return unix.Close(handle.queue)
}
