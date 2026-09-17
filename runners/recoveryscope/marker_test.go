package recoveryscope

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInheritedMarkerLayout(t *testing.T) {
	id, namespace := strings.Repeat("a", 64), strings.Repeat("b", 64)
	pid := strconv.Itoa(os.Getpid())
	tagged := func(digests ...string) string {
		return pid + ":" + markerVersion + ":" + strings.Join(digests, ":")
	}
	for _, tc := range []struct {
		name, marker, want, namespace string
		wantError                     bool
	}{
		{name: "absent"},
		{name: "scope and namespace", marker: tagged(id, namespace), want: id, namespace: namespace},
		// A host with no durable identity projects an empty namespace field.
		{name: "scope without namespace", marker: tagged(id, ""), want: id},
		{name: "stale parent", marker: Marker(999999999, id, namespace), wantError: true},
		{name: "zero owner cannot claim init", marker: Marker(0, id, namespace), wantError: true},
		{name: "malformed identity", marker: tagged("bad", namespace), wantError: true},
		{name: "malformed namespace", marker: tagged(id, "bad"), wantError: true},
		{name: "missing namespace field", marker: tagged(id), wantError: true},
		{name: "trailing field", marker: tagged(id, namespace, strings.Repeat("e", 64)), wantError: true},
		{name: "malformed marker", marker: "bad", wantError: true},
		// A revision older than the layout tag projects the exact scope alone. It
		// stays honored, but never delegates cross-scope recovery.
		{name: "untagged exact scope", marker: pid + ":" + id, want: id},
		// A revision older than the tag also wrote pid:scope:group here. Reading
		// that group as a namespace stamped the container with ownership no sweep
		// could ever match, leaking it permanently — refuse instead of guessing.
		{name: "untagged trailing field", marker: pid + ":" + id + ":" + strings.Repeat("d", 64), wantError: true},
		{name: "untagged malformed scope", marker: pid + ":bad", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvironmentVariable, tc.marker)
			gotID, gotNamespace, err := Inherited()
			if tc.wantError {
				require.Error(t, err)
				require.Empty(t, gotID)
				require.Empty(t, gotNamespace)
				require.Empty(t, Acknowledgement())
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, gotID)
			require.Equal(t, tc.namespace, gotNamespace)
		})
	}
}

func TestAcknowledgement(t *testing.T) {
	id := strings.Repeat("a", 64)
	t.Run("absent", func(t *testing.T) {
		t.Setenv(EnvironmentVariable, "")
		require.Empty(t, Acknowledgement())
	})
	t.Run("echoes an empty namespace", func(t *testing.T) {
		// A compatible agent on a host with no durable identity must not look
		// like an agent that failed to understand the marker at all.
		t.Setenv(EnvironmentVariable, Marker(os.Getpid(), id, ""))
		require.Equal(t, id+":", Acknowledgement())
	})
	t.Run("echoes the complete identity", func(t *testing.T) {
		namespace := strings.Repeat("b", 64)
		t.Setenv(EnvironmentVariable, Marker(os.Getpid(), id, namespace))
		require.Equal(t, id+":"+namespace, Acknowledgement())
	})
}
