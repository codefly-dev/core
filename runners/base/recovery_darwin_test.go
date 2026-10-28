package base

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRecoveryDarwinLegacyIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path, _ := recoveryOrphan(t, false)
	rec, _, err := readPgidRecord(path)
	require.NoError(t, err)
	boot, err := unix.SysctlTimeval("kern.boottime")
	require.NoError(t, err)
	rec.Leader.BootID = fmt.Sprintf("%d:%d", boot.Sec, boot.Usec)
	rec.Owner.BootID = rec.Leader.BootID
	dir, err := pgidNamespaceDir(pgidPreviousNamespace)
	require.NoError(t, err)
	oldPath := filepath.Join(dir, filepath.Base(path))
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(oldPath, raw, 0600))
	require.NoError(t, os.Remove(path))
	group, err := LookupProcessGroup(pid)
	require.NoError(t, err)
	_, err = group.InspectOwnership(t.Context())
	require.ErrorContains(t, err, "incompatible boot identity")
	expected, err := PreviewProcessGroupRecovery(t.Context(), pid)
	require.NoError(t, err)
	require.Equal(t, rec.Leader.BootID, expected.Leader.BootID)
	require.NotEqual(t, expected.Leader.BootID, expected.Members[0].BootID)
	after, err := os.ReadFile(oldPath)
	require.NoError(t, err)
	require.Equal(t, raw, after)
	// The explicit path does not normalize a malformed or changed legacy boot.
	for _, bad := range []string{rec.Leader.BootID + "0", "0:0", "not-a-boot"} {
		changed := rec
		changed.Leader.BootID = bad
		changed.Owner.BootID = bad
		bytes, err := json.Marshal(changed)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(oldPath, bytes, 0600))
		_, err = PreviewProcessGroupRecovery(t.Context(), pid)
		require.Error(t, err)
		assertGroupAlive(t, pid)
	}
	require.NoError(t, os.WriteFile(oldPath, raw, 0600))
	result, err := RecoverProcessGroup(t.Context(), *expected)
	require.NoError(t, err)
	require.True(t, result.GroupDead)
	require.True(t, result.RecordRemoved)
	_, err = os.Stat(oldPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// This deterministic source invariant closes the PID-reuse interleaving:
// token(A), birth(B) refuses B; birth(A), token(B) would authorize B's token.
// Real stale-birth and expired-handle tests separately exercise kernel refusal.
func TestDarwinRecoveryDeliveryTokenPrecedesBirthCheck(t *testing.T) {
	source, err := os.ReadFile("process_identity_darwin.go")
	require.NoError(t, err)
	body := string(source[strings.Index(string(source), "func (handle *darwinProcessSignalHandle) Signal("):])
	before := strings.Index(body, "readDarwinProcessUniqueInfo(handle.identity.pid)")
	check := strings.Index(body, "inspectProcess(handle.identity.pid)")
	compare := strings.Index(body, "!current.matches(handle.identity.recorded())")
	deliver := strings.Index(body, "signalProcessBirth(handle.identity.pid, unique.PIDVersion, signal)")
	safeOrder := func(token, birth, match, signal int) bool {
		return token >= 0 && token < birth && birth < match && match < signal
	}
	require.True(t, safeOrder(before, check, compare, deliver), "PID-reuse safety requires token acquisition before expected-birth verification before delivery")
	// Negative ordering from the old reader must never satisfy the guard.
	require.False(t, safeOrder(check, before, compare, deliver))
}
