package proto

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

// scriptedProc fails its first failures runs, then succeeds. Only Run is
// called; the embedded nil Proc panics on anything else.
type scriptedProc struct {
	base.Proc
	starter *scriptedStarter
}

func (p *scriptedProc) Run(context.Context) error {
	p.starter.runs++
	if p.starter.runs <= p.starter.failures {
		return errors.New("Failure: the server hosted at that remote is unavailable.")
	}
	return nil
}

type scriptedStarter struct {
	failures, runs int
	commands       [][]string
}

func (s *scriptedStarter) NewProcess(bin string, args ...string) (base.Proc, error) {
	s.commands = append(s.commands, append([]string{bin}, args...))
	return &scriptedProc{starter: s}, nil
}

func withoutBackoff(t *testing.T) {
	t.Helper()
	previous := depUpdateBackoff
	depUpdateBackoff = time.Millisecond
	t.Cleanup(func() { depUpdateBackoff = previous })
}

// One Unavailable answer from the Buf Schema Registry used to fail a whole
// sync: a transient registry failure is retried, and a run that then succeeds
// is a success.
func TestBufDependencyUpdateRetriesATransientFailure(t *testing.T) {
	withoutBackoff(t)
	starter := &scriptedStarter{failures: 2}
	require.NoError(t, updateBufDependencies(t.Context(), starter))
	require.Equal(t, 3, starter.runs)
	for _, command := range starter.commands {
		require.Equal(t, []string{"buf", "dep", "update"}, command)
	}
}

// The retry is bounded: a failure that persists is reported with its error,
// never swallowed and never retried forever.
func TestBufDependencyUpdateReportsAPersistentFailure(t *testing.T) {
	withoutBackoff(t)
	starter := &scriptedStarter{failures: depUpdateAttempts}
	err := updateBufDependencies(t.Context(), starter)
	require.ErrorContains(t, err, "cannot update buf")
	require.ErrorContains(t, err, "remote is unavailable")
	require.Equal(t, depUpdateAttempts, starter.runs)
}

// A cancelled caller stops the retries instead of waiting out the backoff.
func TestBufDependencyUpdateStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	starter := &scriptedStarter{failures: depUpdateAttempts}
	require.ErrorContains(t, updateBufDependencies(ctx, starter), "cannot update buf")
	require.Equal(t, 1, starter.runs)
}
