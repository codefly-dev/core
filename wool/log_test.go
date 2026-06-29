package wool_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// capture is a LogProcessor that records every Log it receives, so tests can
// assert on what actually reached the sink after level filtering.
type capture struct {
	mu   sync.Mutex
	logs []*wool.Log
}

func (c *capture) Process(msg *wool.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, msg)
}

func (c *capture) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, l := range c.logs {
		out = append(out, l.Message)
	}
	return out
}

func newWool(t *testing.T, level wool.Loglevel) (*wool.Wool, *capture) {
	t.Helper()
	cap := &capture{}
	w := wool.Get(context.Background()).WithLogger(cap)
	w.WithLoglevel(level)
	return w, cap
}

func TestLogField_OmitsEmptyValues(t *testing.T) {
	log := &wool.Log{
		Level:   wool.INFO,
		Message: "Found configurations",
		Fields: []*wool.LogField{
			wool.Field("configurations", ""),
			wool.Field("count", 3),
		},
	}
	s := log.String()
	require.NotContains(t, s, "configurations=",
		"an empty value must not render a bare key=")
	require.Contains(t, s, "count=3")
}

func TestSliceField_RendersList(t *testing.T) {
	require.Equal(t, "endpoints=[a, b]",
		wool.SliceField("endpoints", []string{"a", "b"}).String())
	require.Equal(t, "endpoints=none",
		wool.SliceField("endpoints", []string{}).String())
}

// stringerEndpoint exercises the fmt.Stringer branch of SliceField's element
// rendering — domain types control their own representation instead of %v.
type stringerEndpoint struct{ name string }

func (e stringerEndpoint) String() string { return e.name }

func TestSliceField_RendersStringerElements(t *testing.T) {
	f := wool.SliceField("endpoints", []stringerEndpoint{{"tcp"}, {"grpc"}})
	require.Equal(t, "endpoints=[tcp, grpc]", f.String())
}

func TestField_NilValueIsDropped(t *testing.T) {
	require.Equal(t, "", wool.Field("k", nil).String(),
		"a nil value must render to nothing so Log.String drops it")
}

func TestSecretField_NeverLeaksValue(t *testing.T) {
	f := wool.SecretField("connection", "postgres://user:hunter2@host/db")
	require.Equal(t, "connection=****", f.String())
	// The raw secret must not survive anywhere on the field — not just in the
	// rendered string but in the Value that reaches structured sinks too.
	require.NotEqual(t, "postgres://user:hunter2@host/db", f.Value)
	require.NotContains(t, f.String(), "hunter2")
}

func TestFocus_OrdersAboveInfo(t *testing.T) {
	require.Greater(t, wool.FOCUS, wool.INFO,
		"FOCUS must outrank INFO so an INFO-level run still shows it")
}

func TestFocus_VisibleAtInfoLevel(t *testing.T) {
	w, cap := newWool(t, wool.INFO)
	w.Focus("milestone")
	w.Info("routine")
	require.Equal(t, []string{"milestone", "routine"}, cap.messages())
}

func TestFocus_HidesRoutineInfoAtFocusLevel(t *testing.T) {
	w, cap := newWool(t, wool.FOCUS)
	w.Info("routine")    // below FOCUS — filtered
	w.Focus("milestone") // shown
	w.Warn("careful")    // above FOCUS — shown
	require.Equal(t, []string{"milestone", "careful"}, cap.messages())
}

func TestLevelFromString(t *testing.T) {
	got, ok := wool.LevelFromString("Debug")
	require.True(t, ok)
	require.Equal(t, wool.DEBUG, got)

	_, ok = wool.LevelFromString("nonsense")
	require.False(t, ok)
}

func TestScopeLevels_OverridePerComponent(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	wool.SetLogScopes("network=debug,*=warn")

	// A network.* scope drops to DEBUG even though the catch-all is WARN.
	netW := wool.Get(context.Background()).In("network.Runtime.GenerateNetworkMappings")
	require.Equal(t, wool.DEBUG, netW.LogLevel())

	// Anything else falls back to the catch-all.
	other := wool.Get(context.Background()).In("resources.Service.Save")
	require.Equal(t, wool.WARN, other.LogLevel())
}

func TestScopeLevels_FilterAtSink(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	wool.SetLogScopes("network=debug,*=warn")

	cap := &capture{}
	base := wool.Get(context.Background()).WithLogger(cap)

	base.In("network.Connect").Debug("dialing") // network scope at DEBUG — shown
	base.In("resources.Load").Debug("loading")  // catch-all WARN — filtered
	base.In("resources.Load").Error("disk")     // catch-all WARN — shown

	require.Equal(t, []string{"dialing", "disk"}, cap.messages())
}

func TestScopeLevels_LongestPrefixWins(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	wool.SetLogScopes("network=warn,network.dns=trace")

	dns := wool.Get(context.Background()).In("network.dns.Resolve")
	require.Equal(t, wool.TRACE, dns.LogLevel())

	other := wool.Get(context.Background()).In("network.Connect")
	require.Equal(t, wool.WARN, other.LogLevel())
}

func TestScopeLevels_MatchOnSegmentBoundary(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	wool.SetLogScopes("net=debug")

	// A prefix must align with a scope segment: "net" matches "net.X" but not
	// "network.X" — otherwise turning up one component leaks into its neighbors.
	require.Equal(t, wool.DEBUG,
		wool.Get(context.Background()).In("net.Dial").LogLevel())
	require.Equal(t, wool.GlobalLogLevel(),
		wool.Get(context.Background()).In("network.Dial").LogLevel())

	// The "::" separator (used by some scopes) anchors too.
	wool.SetLogScopes("RuntimeInstance=debug")
	require.Equal(t, wool.DEBUG,
		wool.Get(context.Background()).In("RuntimeInstance::Load").LogLevel())
}

func TestScopeLevels_InstanceLevelTakesPrecedence(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	wool.SetLogScopes("network=debug")

	w := wool.Get(context.Background()).In("network.Connect")
	w.WithLoglevel(wool.ERROR)
	// An explicit per-instance level wins over a scope override — this is the
	// contract custom processors rely on to receive every line.
	require.Equal(t, wool.ERROR, w.LogLevel())
}

func TestScopeLevels_IgnoresMalformedEntries(t *testing.T) {
	t.Cleanup(func() { wool.SetLogScopes("") })
	// "=warn" has an empty name and must NOT become a catch-all.
	wool.SetLogScopes("=warn,network=debug")

	require.Equal(t, wool.GlobalLogLevel(),
		wool.Get(context.Background()).In("resources.Load").LogLevel())
	require.Equal(t, wool.DEBUG,
		wool.Get(context.Background()).In("network.Dial").LogLevel())
}

func TestString_DropsEmptyFieldsEndToEnd(t *testing.T) {
	w, cap := newWool(t, wool.INFO)
	w.Info("done", wool.Field("a", ""), wool.Field("b", "x"))
	require.Len(t, cap.logs, 1)
	line := cap.logs[0].String()
	require.Contains(t, line, "b=x")
	require.False(t, strings.Contains(line, "a="), "empty field a= should be dropped: %s", line)
}
