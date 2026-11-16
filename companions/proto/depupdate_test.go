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
	if p.starter.onRun != nil {
		p.starter.onRun()
	}
	if p.starter.runs <= p.starter.failures {
		return errors.New("Failure: the server hosted at that remote is unavailable.")
	}
	return nil
}

type scriptedStarter struct {
	failures, runs int
	commands       [][]string
	onRun          func()
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
	require.ErrorContains(t, err, "failed after 3 attempt(s)")
	require.ErrorContains(t, err, "remote is unavailable")
	require.Equal(t, depUpdateAttempts, starter.runs)
}

// A caller that cancels while a retry is pending stops the retries at once
// instead of waiting out the backoff. Whether the cancel lands during the run
// or during the wait, the outcome is the same: one run, and an error saying
// the caller stopped it.
func TestBufDependencyUpdateStopsWhenTheCallerCancels(t *testing.T) {
	previous := depUpdateBackoff
	depUpdateBackoff = time.Hour
	t.Cleanup(func() { depUpdateBackoff = previous })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	starter := &scriptedStarter{failures: depUpdateAttempts}
	starter.onRun = func() { time.AfterFunc(10*time.Millisecond, cancel) }
	done := make(chan error, 1)
	go func() { done <- updateBufDependencies(ctx, starter) }()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "stopped by the caller")
		require.Equal(t, 1, starter.runs)
	case <-time.After(10 * time.Second):
		t.Fatal("the retry waited out its backoff after the caller cancelled")
	}
}
