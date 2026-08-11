//go:build darwin

package base

import (
	"errors"
	"fmt"
	"syscall"
	"time"

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

func currentProcessBoundary() (processBoundary, error) {
	bootID, err := darwinBootID()
	if err != nil {
		return processBoundary{}, err
	}
	now := time.Now()
	return processBoundary{
		BootID:  bootID,
		StartID: uint64(now.Unix())*1_000_000 + uint64(now.Nanosecond()/1_000),
	}, nil
}

func darwinBootID() (string, error) {
	bootTime, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", bootTime.Sec, bootTime.Usec), nil
}
