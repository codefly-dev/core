//go:build darwin

package base

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

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
		if darwinProcessNotFound(err) {
			return processIdentity{}, errProcessNotFound
		}
		return processIdentity{}, err
	}
	executablePath, err := proc.Exe()
	if err != nil {
		if darwinProcessNotFound(err) {
			return processIdentity{}, errProcessNotFound
		}
		// A running executable may be unlinked after launch (for example when a
		// test removes its temporary build directory). Darwin then returns
		// ENOENT for the path even though the PID and process-group membership
		// remain live. The kinfo command name is sufficient for diagnostics;
		// boot id + start id are the authentication identity.
		if !errors.Is(err, syscall.ENOENT) {
			return processIdentity{}, err
		}
		executablePath = darwinProcessCommand(info)
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

// darwinProcessNotFound normalizes disappearance contracts that prove the PID
// no longer exists. ENOENT is deliberately excluded: Exe also returns it for a
// live process whose backing executable was unlinked.
func darwinProcessNotFound(err error) bool {
	return errors.Is(err, process.ErrorProcessNotRunning) ||
		errors.Is(err, syscall.ESRCH)
}

func darwinProcessCommand(info *unix.KinfoProc) string {
	if info == nil {
		return ""
	}
	command := make([]byte, 0, len(info.Proc.P_comm))
	for _, character := range info.Proc.P_comm {
		if character == 0 {
			break
		}
		command = append(command, byte(character))
	}
	return string(command)
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
	token darwinAuditToken
}

func openProcessSignalHandle(expected processIdentity) (processSignalHandle, error) {
	unique, err := readDarwinProcessUniqueInfo(expected.pid)
	if err != nil {
		return nil, err
	}
	current, err := inspectProcess(expected.pid)
	if err != nil {
		return nil, err
	}
	if current.pgid != expected.pgid || !current.matches(expected.recorded()) {
		return nil, errProcessGroupIdentityChanged
	}
	token := darwinAuditToken{}
	token.Value[5] = uint32(expected.pid)
	token.Value[7] = uint32(unique.PIDVersion)
	return &darwinProcessSignalHandle{token: token}, nil
}

func (handle *darwinProcessSignalHandle) Signal(signal syscall.Signal) error {
	const procInfoCallSignalAuditToken = 0x11
	_, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procInfoCallSignalAuditToken,
		0,
		uintptr(signal),
		0,
		uintptr(unsafe.Pointer(&handle.token)),
		unsafe.Sizeof(handle.token),
	)
	runtime.KeepAlive(handle)
	if errno != 0 {
		return errno
	}
	return nil
}

func (handle *darwinProcessSignalHandle) Close() error {
	return nil
}

type darwinAuditToken struct {
	Value [8]uint32
}

type darwinProcessUniqueInfo struct {
	ExecutableUUID           [16]byte
	UniqueID                 uint64
	ParentUniqueID           uint64
	PIDVersion               int32
	OriginalParentPIDVersion int32
	Reserved                 [2]uint64
}

func readDarwinProcessUniqueInfo(pid int) (darwinProcessUniqueInfo, error) {
	const (
		procInfoCallPIDInfo             = 0x2
		procPIDUniqueIdentifierInfo     = 17
		procPIDUniqueIdentifierInfoSize = 56
	)
	var info darwinProcessUniqueInfo
	written, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procInfoCallPIDInfo,
		uintptr(pid),
		procPIDUniqueIdentifierInfo,
		0,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	runtime.KeepAlive(&info)
	if errno != 0 {
		if errors.Is(errno, syscall.ESRCH) {
			return darwinProcessUniqueInfo{}, errProcessNotFound
		}
		return darwinProcessUniqueInfo{}, errno
	}
	if written != procPIDUniqueIdentifierInfoSize || info.PIDVersion <= 0 {
		return darwinProcessUniqueInfo{}, errors.New("process unique identity is incomplete")
	}
	return info, nil
}
