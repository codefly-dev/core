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

// readProcessGroupAuthentication reads pid's start credential and reports
// whether pid's environment was observable at all.
//
// Darwin decides that per target rather than per caller: kern.procargs2 stops
// the copyout at the end of argv unless the target is the caller itself, the
// target is not code-signing restricted, SIP is off, or the caller holds
// com.apple.private.read-environment-variables. Being root is not on that list
// — it only buys the right to call procargs2 across uids at all. So an Apple
// platform binary's environment is never visible here while an ordinary
// service binary's is, and a truncated read means the credential is unknown
// rather than absent.
func readProcessGroupAuthentication(pid int) (string, bool, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		// procargs2 reports a pid it cannot find as EINVAL, not ESRCH: the
		// sysctl fails its proc_find before it can distinguish "gone" from a
		// malformed request. A member that exits between enumeration and this
		// read is the ordinary case mid-escalation, so it must read as a
		// disappearance rather than as a failure to authenticate the group.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EINVAL) {
			return "", false, errProcessNotFound
		}
		return "", false, err
	}
	if len(data) < 4 {
		return "", false, errors.New("process arguments are incomplete")
	}
	argc := int(binary.NativeEndian.Uint32(data[:4]))
	data = data[4:]
	executableEnd := bytes.IndexByte(data, 0)
	if executableEnd < 0 {
		return "", false, errors.New("process executable is unterminated")
	}
	data = bytes.TrimLeft(data[executableEnd+1:], "\x00")
	for range argc {
		argumentEnd := bytes.IndexByte(data, 0)
		if argumentEnd < 0 {
			return "", false, errors.New("process arguments are unterminated")
		}
		data = data[argumentEnd+1:]
	}
	// The kernel ends the copyout at the last argv terminator when it withholds
	// the environment, so nothing remaining here means nothing was disclosed.
	if len(data) == 0 {
		return "", false, nil
	}
	prefix := []byte(groupAuthEnv + "=")
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if value, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(value), true, nil
		}
	}
	return "", true, nil
}

type darwinProcessSignalHandle struct {
	identity processIdentity
}

func openProcessSignalHandle(expected processIdentity) (processSignalHandle, error) {
	if _, err := readDarwinProcessUniqueInfo(expected.pid); err != nil {
		return nil, err
	}
	current, err := inspectProcess(expected.pid)
	if err != nil {
		return nil, err
	}
	if current.pgid != expected.pgid || !current.matches(expected.recorded()) {
		return nil, errProcessGroupIdentityChanged
	}
	return &darwinProcessSignalHandle{identity: expected}, nil
}

// darwinSignalAttempts bounds the re-resolution in Signal. Every retry is
// caused by one exec the target ran underneath us, or by a read that raced
// that same exec, and a starting process execs a small, finite number of
// times.
const darwinSignalAttempts = 8

// Signal delivers to the exact process birth this handle names.
//
// Darwin advances p_idversion on every exec, not only at fork, so an audit
// token minted before the target execs names an incarnation that no longer
// exists and the kernel rejects the delivery with ESRCH. A caller cannot tell
// that apart from the process having exited, so it moves on and a live process
// survives a pass that reported no failure. The birth is therefore re-verified
// for every attempt and the version read immediately before delivery, leaving
// no room for an exec to land between the two.
//
// Everything the exec window itself produces — a rejected token, a read that
// raced the exec — is retried rather than reported, because that window is
// exactly when these reads are unstable; the last failure is reported only
// once the attempts are spent.
//
// A member that is gone, or that is no longer the birth we were told to
// signal, is reported as ESRCH. That is a statement about this one member and
// callers read it as "nothing to do here". It must never be
// errProcessGroupIdentityChanged: that is a verdict on the whole group, and
// terminateGroup abandons the escalation when it sees one.
func (handle *darwinProcessSignalHandle) Signal(signal syscall.Signal) error {
	var last error
	for range darwinSignalAttempts {
		current, err := inspectProcess(handle.identity.pid)
		if errors.Is(err, errProcessNotFound) {
			return syscall.ESRCH
		}
		if err != nil {
			last = err
			continue
		}
		if current.pgid != handle.identity.pgid || !current.matches(handle.identity.recorded()) {
			return syscall.ESRCH
		}
		unique, err := readDarwinProcessUniqueInfo(handle.identity.pid)
		if errors.Is(err, errProcessNotFound) {
			return syscall.ESRCH
		}
		if err != nil {
			last = err
			continue
		}
		err = signalProcessBirth(handle.identity.pid, unique.PIDVersion, signal)
		if errors.Is(err, syscall.ESRCH) {
			last = err
			continue
		}
		return err
	}
	return fmt.Errorf("deliver signal %d to process %d: %d attempts exhausted: %w",
		signal, handle.identity.pid, darwinSignalAttempts, last)
}

func signalProcessBirth(pid int, version int32, signal syscall.Signal) error {
	const procInfoCallSignalAuditToken = 0x11
	token := darwinAuditToken{}
	token.Value[5] = uint32(pid)
	token.Value[7] = uint32(version)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procInfoCallSignalAuditToken,
		0,
		uintptr(signal),
		0,
		uintptr(unsafe.Pointer(&token)),
		unsafe.Sizeof(token),
	)
	runtime.KeepAlive(&token)
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
